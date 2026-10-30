package cmd

import (
	"charm.land/lipgloss/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/traust-security/traust-cli/internal/harness"
	"github.com/traust-security/traust-cli/internal/theme"
	"github.com/traust-security/traust-cli/internal/tui"
)

// peekTail is how many journal entries a peek shows.
const peekTail = 12

var peekCmd = &cobra.Command{
	Use:   "peek <run-or-flow>",
	Short: "Snapshot a run's (or flow's) current state and recent output",
	Long: `A one-shot look at what is happening right now: state, usage and the
last few journal entries — printed and gone, no following. For live
viewing use attach; for the full transcript use logs.`,
	Args: cobra.ExactArgs(1),
	Run:  runPeek,
}

func init() {
	rootCmd.AddCommand(peekCmd)
}

func runPeek(cmd *cobra.Command, args []string) {
	state, err := loadState()
	if err != nil {
		fatalf(1, "%v", err)
	}

	// Runs first, then flows — a peek is equally valid on either.
	if info, err := resolveRun(state.RunsDir(), args[0]); err == nil {
		lipgloss.Fprint(os.Stdout, peekRun(info))
		return
	}
	if m, err := resolveFlowE(flowsDir(state.Workspace), args[0]); err == nil {
		lipgloss.Fprint(os.Stdout, peekFlow(m, state.RunsDir()))
		return
	}
	fatalf(1, "no run or flow matches %q — see `traust-cli runs` / `traust-cli flow status`", args[0])
}

// peekRun renders a run snapshot: header line, then the journal tail.
func peekRun(info *RunInfo) string {
	var b strings.Builder
	styles := theme.Default()

	stateStyle := styles.OK
	switch info.State {
	case stateFailed, stateOrphaned:
		stateStyle = styles.Err
	case stateRunning:
		stateStyle = styles.Accent
	case stateAborted, stateBudget, stateNeedsIn:
		stateStyle = styles.Warn
	}
	head := fmt.Sprintf("%s %s · %s · %s", shortRunID(info.ID), info.Skill, stateStyle.Render(info.State), humanAgeSince(info))
	if info.Tokens.In+info.Tokens.Out > 0 {
		head += fmt.Sprintf(" · in %dk out %dk", info.Tokens.In/1000, info.Tokens.Out/1000)
	}
	b.WriteString(styles.Header.Render(head) + "\n")

	for _, line := range journalTailLines(filepath.Join(info.Dir, "stream.jsonl"), peekTail) {
		b.WriteString(line)
	}

	switch info.State {
	case stateRunning:
		b.WriteString(styles.Muted.Render(fmt.Sprintf("… live: traust-cli attach %s", shortRunID(info.ID))) + "\n")
	case stateOrphaned:
		b.WriteString(styles.Warn.Render("orphaned: the worker died without writing a result") + "\n")
	}
	return b.String()
}

// peekFlow renders a flow snapshot: the stage chain, then a peek of the
// current (or last) stage's run.
func peekFlow(m *flowManifest, runsDir string) string {
	var b strings.Builder
	styles := theme.Default()

	stateStyle := styles.OK
	if m.State == stateFailed {
		stateStyle = styles.Err
	} else if m.State == stateRunning {
		stateStyle = styles.Accent
	}
	b.WriteString(styles.Header.Render(fmt.Sprintf("flow %s · %s · %s · %s", shortFlowID(m.ID), m.Flow, stateStyle.Render(m.State), m.Target)) + "\n")

	var names []string
	for _, s := range m.Stages {
		mark := "✓"
		switch s.State {
		case stateFailed:
			mark = "✗"
		case stateRunning:
			mark = "▸"
		case "pending":
			mark = "·"
		case "skipped":
			mark = "-"
		}
		names = append(names, fmt.Sprintf("%s %s", mark, s.Name))
	}
	b.WriteString(styles.Muted.Render(strings.Join(names, " → ")) + "\n")

	if cur := m.currentStage(); cur != nil && cur.RunID != "" {
		if info, err := loadRunInfo(filepath.Join(runsDir, cur.RunID)); err == nil && info != nil {
			b.WriteString("\n")
			b.WriteString(peekRun(info))
		}
	}
	return b.String()
}

// journalTailLines renders the last n entries of a stream journal as
// display lines, collapsing keyed streaming updates to their final content.
func journalTailLines(path string, n int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{theme.Default().Muted.Render("  (no journal yet)") + "\n"}
	}
	lines := splitLines(string(data))
	start := len(lines) - n
	if start < 0 {
		start = 0
	}
	var buf strings.Builder
	events := newKeyedRenderer(&buf)
	for _, line := range lines[start:] {
		if it, ok := journalLineToItem(line); ok {
			switch it.Kind {
			case tui.ItemEvent:
				events.Event(it.Event)
			case tui.ItemRaw:
				events.Flush()
				if it.Text != "" {
					buf.WriteString(tui.Wrap(theme.Default().Muted.Render("  "+it.Text), tui.TerminalWidth()) + "\n")
				}
			}
		}
	}
	events.Flush()
	if buf.Len() == 0 {
		return nil
	}
	return []string{buf.String()}
}

func humanAgeSince(info *RunInfo) string {
	if info.StartedAt.IsZero() {
		return "age ?"
	}
	return humanAge(time.Since(info.StartedAt))
}

// renderEventString renders one harness event as display lines (the string
// form of renderPlainEvent).
func renderEventString(ev harness.Event) string {
	styles := theme.Default()
	switch ev.Kind {
	case harness.EventText:
		return tui.Wrap(ev.Text, tui.TerminalWidth()) + "\n"
	case harness.EventThinking:
		return tui.Wrap(styles.Muted.Render("· "+ev.Text), tui.TerminalWidth()) + "\n"
	case harness.EventToolCall:
		line := "→ " + ev.Tool
		if ev.Text != "" {
			line += "  " + ev.Text
		}
		return tui.Wrap(styles.Accent.Render(line), tui.TerminalWidth()) + "\n"
	case harness.EventToolResult:
		style := styles.Muted
		if ev.IsError {
			style = styles.Warn
		}
		var b strings.Builder
		for _, l := range strings.Split(ev.Text, "\n") {
			b.WriteString(tui.Wrap(style.Render("  │ "+l), tui.TerminalWidth()) + "\n")
		}
		return b.String()
	case harness.EventError:
		return tui.Wrap(styles.Err.Render("✗ "+ev.Text), tui.TerminalWidth()) + "\n"
	}
	return ""
}
