package cmd

import (
	"charm.land/lipgloss/v2"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/traust-security/traust-cli/internal/harness"
	"github.com/traust-security/traust-cli/internal/runner"
	"github.com/traust-security/traust-cli/internal/theme"
	"github.com/traust-security/traust-cli/internal/tui"
)

var attachCmd = &cobra.Command{
	Use:   "attach <run>",
	Short: "Watch a run live (Ctrl-C detaches without cancelling)",
	Long: `Attach to a background run: replay its journal from the start and follow
it live. Ctrl-C, q or d detaches — the run keeps going in its own process.
x twice cancels the run. Attaching to a finished run replays the whole
transcript and shows the result. Without a terminal, attach degrades to a
plain live tail.`,
	Args: cobra.ExactArgs(1),
	Run:  runAttach,
}

func init() {
	rootCmd.AddCommand(attachCmd)
}

func runAttach(cmd *cobra.Command, args []string) {
	state, err := loadState()
	if err != nil {
		fatalf(1, "%v", err)
	}
	info, err := resolveRun(state.RunsDir(), args[0])
	if err != nil {
		fatalf(1, "%v", err)
	}
	attachByID(info)
}

// attachByID runs the attach flow over a resolved run, then exits with the
// run's own code when it finished while attached.
func attachByID(info *RunInfo) {
	attachOnce(info)
	printAttachExit(info)
}

// attachOnce attaches without any exit handling — the pipeline follower
// reuses it stage after stage.
func attachOnce(info *RunInfo) {
	// Header from the run's spec.
	header := tui.MonitorHeader{Skill: info.Skill, RunID: info.ID}
	if data, err := os.ReadFile(filepath.Join(info.Dir, "spec.json")); err == nil {
		var spec runner.Spec
		if json.Unmarshal(data, &spec) == nil {
			header.Harness = spec.Harness
			header.Model = spec.Model
			header.BudgetCap = spec.BudgetCap
			if t, err := time.Parse(time.RFC3339, spec.StartedAt); err == nil {
				header.StartedAt = t
			}
		}
	}

	if !isTTY() {
		plainAttach(info)
		return
	}

	follow := make(chan tui.MonitorItem, 128)
	go func() {
		tailJournal(info.Dir, follow)
	}()

	_, err := tui.Attach(header, follow, func() { stopRun(info) })
	if err != nil {
		fatalf(1, "attach: %v", err)
	}
}

// plainAttach is the non-TTY tail: replay + follow until the run finishes.
func plainAttach(info *RunInfo) {
	styles := theme.Default()
	lipgloss.Fprintln(os.Stdout, styles.Accent.Render(fmt.Sprintf("▸ attaching to %s (%s) — Ctrl-C detaches", info.ID, info.State)))
	events := newKeyedRenderer(os.Stdout)
	items := make(chan tui.MonitorItem, 128)
	go func() {
		tailJournal(info.Dir, items)
	}()
	for it := range items {
		switch it.Kind {
		case tui.ItemEvent:
			events.Event(it.Event)
		case tui.ItemRaw:
			events.Flush()
			if it.Text != "" {
				lipgloss.Fprintln(os.Stdout, tui.Wrap(styles.Muted.Render("  "+it.Text), tui.TerminalWidth()))
			}
		case tui.ItemProgress:
			events.Flush()
			lipgloss.Fprintln(os.Stdout, tui.Wrap(styles.Accent.Render("› "+it.Text), tui.TerminalWidth()))
		case tui.ItemDone:
			events.Flush()
			lipgloss.Fprintln(os.Stdout, styles.Muted.Render("— run complete —"))
		}
	}
	events.Flush()
	printResultFromDir(info.Dir)
}

// tailJournal replays the run's stream.jsonl from the start and follows it
// until result.json appears (or the run is already finished). Header usage
// is refreshed in passing (budget cap arrives with spec.json).
func tailJournal(dir string, out chan<- tui.MonitorItem) {
	defer close(out)
	stream := filepath.Join(dir, "stream.jsonl")

	// Wait briefly for the journal to exist (the worker may still be in
	// pre-flight when attach races the start).
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(stream); err == nil {
			break
		}
		if resultExists(dir) || time.Now().After(deadline) {
			emitResultLines(dir, out)
			return
		}
		time.Sleep(150 * time.Millisecond)
	}

	tailer, err := runner.NewTailer(stream, 0)
	if err != nil {
		out <- tui.MonitorItem{Kind: tui.ItemProgress, Text: "warning: cannot open journal: " + err.Error()}
		emitResultLines(dir, out)
		return
	}
	defer tailer.Close()

	for {
		lines, err := tailer.Read()
		if err != nil {
			out <- tui.MonitorItem{Kind: tui.ItemProgress, Text: "warning: journal read error: " + err.Error()}
			break
		}
		for _, line := range lines {
			if it, ok := journalLineToItem(line); ok {
				out <- it
			}
		}
		if resultExists(dir) {
			// Drain any final lines, then finish.
			for {
				lines, err := tailer.Read()
				if err != nil || len(lines) == 0 {
					break
				}
				for _, line := range lines {
					if it, ok := journalLineToItem(line); ok {
						out <- it
					}
				}
			}
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	emitResultLines(dir, out)
}

func resultExists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "result.json"))
	return err == nil
}

// journalLineToItem converts one stream.jsonl line into a monitor item.
func journalLineToItem(line string) (tui.MonitorItem, bool) {
	entry, err := runner.ParseEntry([]byte(line))
	if err != nil {
		return tui.MonitorItem{}, false
	}
	switch entry.Kind {
	case "event":
		if entry.Event == nil {
			return tui.MonitorItem{}, false
		}
		return tui.MonitorItem{Kind: tui.ItemEvent, Event: harnessEvent(*entry.Event)}, true
	case "raw":
		return tui.MonitorItem{Kind: tui.ItemRaw, Text: entry.Raw}, true
	}
	return tui.MonitorItem{}, false
}

// harnessEvent widens a journal event record into the harness event shape
// (identical fields, different types for the kind).
func harnessEvent(e runner.Event) harness.Event {
	return harness.Event{
		Kind:      harness.EventKind(e.Kind),
		Text:      e.Text,
		Tool:      e.Tool,
		TokensIn:  e.TokensIn,
		TokensOut: e.TokensOut,
		CostUSD:   e.CostUSD,
		IsError:   e.IsError,
		Key:       e.Key,
	}
}

// emitResultLines appends terminal progress lines from result.json.
func emitResultLines(dir string, out chan<- tui.MonitorItem) {
	data, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err != nil {
		out <- tui.MonitorItem{Kind: tui.ItemProgress, Text: "warning: run ended without a result (orphaned worker?)"}
		return
	}
	var res runner.RunResult
	if json.Unmarshal(data, &res) != nil {
		return
	}
	for _, a := range res.Artifacts {
		mark := "artifact"
		if !a.Valid {
			mark = "artifact failed schema validation"
		}
		out <- tui.MonitorItem{Kind: tui.ItemProgress, Text: fmt.Sprintf("%s: %s", mark, a.Path)}
	}
	if res.HarnessError != "" {
		out <- tui.MonitorItem{Kind: tui.ItemProgress, Text: "harness error: " + res.HarnessError}
	}
}

func printAttachExit(info *RunInfo) {
	styles := theme.Default()
	short := shortRunID(info.ID)
	refreshed, err := loadRunInfo(info.Dir)
	if err == nil && refreshed != nil && refreshed.State != stateRunning {
		// The run finished while attached: show the summary.
		printResultFromDir(info.Dir)
		return
	}
	lipgloss.Fprintln(os.Stdout)
	lipgloss.Fprintln(os.Stdout, styles.OK.Render("▸ detached — the run continues in the background"))
	kv("re-attach", "traust-cli attach "+short)
	kv("result", "traust-cli result "+short)
}

// printResultFromDir renders the stored result.json as the run summary.
func printResultFromDir(dir string) {
	data, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err != nil {
		fatalf(1, "no result for this run (orphaned?) — journal: %s", dir)
	}
	var res runner.RunResult
	if err := json.Unmarshal(data, &res); err != nil {
		fatalf(1, "parse result.json: %v", err)
	}
	printRunSummary(&res)
}
