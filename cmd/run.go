package cmd

import (
	"charm.land/lipgloss/v2"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/traust-security/traust-cli/internal/api"
	"github.com/traust-security/traust-cli/internal/bootstrap"
	"github.com/traust-security/traust-cli/internal/catalog"
	"github.com/traust-security/traust-cli/internal/harness"
	"github.com/traust-security/traust-cli/internal/runner"
	"github.com/traust-security/traust-cli/internal/theme"
	"github.com/traust-security/traust-cli/internal/tui"
)

var runFlags struct {
	harnessID            string
	model                string
	maxTurns             int
	unsafeFullPermission bool
	budgetOverride       string
	json                 bool
	strictValidation     bool
	noMonitor            bool
	detach               bool
}

var runCmd = &cobra.Command{
	Use:   "run [flags] <skill> [args…]",
	Short: "Run a skill headlessly through your harness",
	Long: `Composes the skill's SKILL.md into a prompt, resolves the model-floor
default from the registry, and drives the harness of your choice in headless
mode. The agent always runs from the harness root — never inside the audit
target (rule S9) — and every output line is journaled under
~/.traust/cli/runs/.

Flags come BEFORE the skill name; everything after it is passed through to
the skill verbatim, including its own flags:

  traust-cli run triage <report.json> --auto
  traust-cli run --harness crush vuln-scan /src/app --diff --since abc

Exit codes: 0 ok · 1 error · 2 the skill needs interactive input · 3 budget
refuse band hit.`,
	Args: cobra.MinimumNArgs(1),
	Run:  runRun,
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringVar(&runFlags.harnessID, "harness", "", "agent harness (claude, codex, pi, opencode, crush, goose; default: state default)")
	runCmd.Flags().StringVar(&runFlags.model, "model", "", "model override (default: the registry floor for the skill's role)")
	runCmd.Flags().IntVar(&runFlags.maxTurns, "max-turns", 0, "cap agent turns (0 = harness default)")
	runCmd.Flags().BoolVar(&runFlags.unsafeFullPermission, "unsafe-full-permission", false, "grant the harness full permission for this run (also settable at init: --full-permission)")
	runCmd.Flags().StringVar(&runFlags.budgetOverride, "budget-override", "", "reason to proceed past the budget refuse band (recorded)")
	runCmd.Flags().BoolVar(&runFlags.json, "json", false, "machine-readable result summary on stdout")
	runCmd.Flags().BoolVar(&runFlags.strictValidation, "strict", false, "strict schema validation of artifacts")
	runCmd.Flags().BoolVar(&runFlags.noMonitor, "no-monitor", false, "plain output even on a TTY (synchronous, in-process)")
	runCmd.Flags().BoolVarP(&runFlags.detach, "detach", "d", false, "start in the background without attaching (interactive runs attach by default)")

	// Git-style passthrough: our flags are parsed only BEFORE the skill
	// name; everything after it — including skill flags like triage's
	// --auto or vuln-scan's --diff --since — is passed through verbatim
	// instead of being rejected as unknown CLI flags.
	runCmd.Flags().SetInterspersed(false)
}

func runRun(cmd *cobra.Command, args []string) {
	state, err := loadState()
	if err != nil {
		fatalf(1, "%v", err)
	}
	cat, err := catalog.Load(state.TraustRepo())
	if err != nil {
		fatalf(1, "%v", err)
	}
	skill, ok := cat.ByName(args[0])
	if !ok {
		fatalf(1, "unknown skill %q — see `traust-cli skills`", args[0])
	}
	executeRun(state, skill, args[1:])
}

// runSkillInteractive is the picker continuation: ask for the invocation
// arguments, then run.
func runSkillInteractive(skill *catalog.Skill) {
	state, err := loadState()
	if err != nil {
		fatalf(1, "%v", err)
	}
	args := ""
	model := ""
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Arguments").
				Description("Passed to the skill as $ARGUMENTS (e.g. a repo path or URL).").
				Placeholder(skill.ArgumentHint).
				Value(&args),
			huh.NewInput().
				Title("Model override (optional)").
				Description("Leave empty to use the registry floor model for the skill's role.").
				Value(&model),
		),
	)
	if err := form.Run(); err != nil {
		fatalf(1, "run form: %v", err)
	}

	var runArgs []string
	if strings.TrimSpace(args) != "" {
		runArgs = strings.Fields(args)
	}
	savedModel := runFlags.model
	runFlags.model = model
	defer func() { runFlags.model = savedModel }()

	executeRun(state, skill, runArgs)
}

func executeRun(state *bootstrap.State, skill *catalog.Skill, args []string) {
	ctx := context.Background()

	harnessID := runFlags.harnessID
	if harnessID == "" {
		harnessID = state.Harness
	}
	h, err := harness.Get(harnessID)
	if err != nil {
		fatalf(1, "%v", err)
	}
	detection := h.Detect(ctx)
	if !detection.Available {
		fatalf(1, "%s binary %q not found — install it or pick another harness with --harness", h.DisplayName(), h.Binary())
	}

	// Full-permission mode: explicit per-run flag, or the init-time consent
	// recorded in state. The harness's skills require unattended tool use.
	unsafeFullPermission := runFlags.unsafeFullPermission || state.FullPermission
	if unsafeFullPermission && !runFlags.unsafeFullPermission {
		warnNote("full-permission mode active (consented at init; re-run `traust-cli init` to change) — harness runs with all tools auto-approved")
	}

	if skill.Standalone == nil {
		warnNote(fmt.Sprintf("%q is not in the standalone profile — it may expect campaign infrastructure (siblings, ledger, tracker)", skill.Name))
	}

	// Interactive terminals launch the run in the BACKGROUND and attach:
	// closing the terminal (or detaching with ctrl+c) leaves the run alive.
	// --detach skips the attach; non-TTY and --json/--no-monitor keep the
	// synchronous in-process semantics (CI needs the exit code and output).
	interactive := isTTY() && !runFlags.json && !runFlags.noMonitor
	if runFlags.detach || interactive {
		runID, err := newRunID(skill.Name)
		if err != nil {
			fatalf(1, "mint run id: %v", err)
		}
		runID, err = startBackgroundRun(state, runRequest{
			RunID:                runID,
			Skill:                skill.Name,
			Args:                 args,
			Harness:              h.ID(),
			Model:                runFlags.model,
			MaxTurns:             runFlags.maxTurns,
			UnsafeFullPermission: unsafeFullPermission,
			BudgetOverride:       runFlags.budgetOverride,
			StrictValidation:     runFlags.strictValidation,
		})
		if err != nil {
			fatalf(1, "start background run: %v", err)
		}
		if !interactive || runFlags.detach {
			if runFlags.json {
				out, _ := json.MarshalIndent(map[string]string{"run_id": runID}, "", "  ")
				lipgloss.Fprintln(os.Stdout, string(out))
			} else {
				printStarted(runID)
			}
			return
		}
		attachRunID(state, runID)
		return
	}

	// Synchronous path.
	opts := runner.Options{
		State:                state,
		API:                  api.NewPythonCLI(state.VenvPython(), state.ChildEnv(), state.TraustRepo()),
		Harness:              h,
		Skill:                skill,
		Args:                 args,
		Model:                runFlags.model,
		MaxTurns:             runFlags.maxTurns,
		UnsafeFullPermission: unsafeFullPermission,
		BudgetOverrideReason: runFlags.budgetOverride,
		StrictValidation:     runFlags.strictValidation,
		DeclareSpend:         true,
	}
	sinkWriter := os.Stdout
	if runFlags.json {
		sinkWriter = os.Stderr
	}
	res, err := runner.Run(ctx, opts, newPlainSink(sinkWriter))
	if err != nil {
		fatalf(1, "run: %v", err)
	}

	if runFlags.json {
		out, _ := json.MarshalIndent(res, "", "  ")
		lipgloss.Fprintln(os.Stdout, string(out))
	} else {
		// The summary prints after the monitor too: artifacts, journal
		// location and the failure reason must not vanish with the TUI.
		printRunSummary(res)
	}
	os.Exit(res.ExitCode)
}

// attachRunID attaches the monitor to a freshly started background run and
// exits with the run's code when it completes while attached.
func attachRunID(state *bootstrap.State, runID string) {
	info, err := loadRunInfo(filepath.Join(state.RunsDir(), runID))
	if err != nil || info == nil {
		fatalf(1, "run %s vanished", runID)
	}
	runAttach(&cobra.Command{}, []string{runID})
}

func printRunSummary(res *runner.RunResult) {
	styles := theme.Default()
	section("Run result")
	kv("run id", res.RunID)
	kv("journal", res.Dir)
	kv("model", res.Model)
	if res.Usage.TokensIn+res.Usage.TokensOut > 0 {
		kv("tokens", fmt.Sprintf("in %d · out %d", res.Usage.TokensIn, res.Usage.TokensOut))
	}
	if res.BudgetCap > 0 {
		kv("budget", fmt.Sprintf("%s band · cap %d output tokens", res.BudgetBand, res.BudgetCap))
	}
	if res.HarnessError != "" {
		lipgloss.Fprintln(os.Stdout)
		lipgloss.Fprintln(os.Stdout, styles.Err.Render("harness error:"))
		lipgloss.Fprintln(os.Stdout, indent(wrapBlock(res.HarnessError, tui.TerminalWidth()-4), "  "))
	}
	if len(res.Artifacts) > 0 {
		lipgloss.Fprintln(os.Stdout)
		for _, a := range res.Artifacts {
			mark := styles.OK.Render("✓")
			if !a.Valid {
				mark = styles.Err.Render("✗")
			}
			extra := ""
			if a.Rendered != "" {
				extra = styles.Muted.Render(" (rendered → " + a.Rendered + ")")
			}
			lipgloss.Fprintln(os.Stdout, tui.Wrap(mark+" "+a.Path+extra, tui.TerminalWidth()))
		}
	}
	if res.ResultText != "" {
		lipgloss.Fprintln(os.Stdout)
		lipgloss.Fprintln(os.Stdout, styles.Muted.Render("final message:"))
		lipgloss.Fprintln(os.Stdout, indent(tui.Wrap(res.ResultText, tui.TerminalWidth()-4), "  "))
	}
}

// wrapBlock normalizes a multi-line block for display: trailing whitespace
// is stripped, leading indentation from source formatting (e.g. a harness's
// styled error box) is dropped so re-wrapped lines start at the left edge,
// and every line wraps to width.
func wrapBlock(s string, width int) string {
	if width < 10 {
		width = 10
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return tui.Wrap(strings.Join(lines, "\n"), width)
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// warnNote prints a wrapped warning to stderr.
func warnNote(msg string) {
	lipgloss.Fprintln(os.Stderr, tui.Wrap(theme.Default().Warn.Render("warning: "+msg), tui.TerminalWidth()))
}

// plainSink prints run updates as styled plain text (non-TTY or --no-monitor).
// Keyed streaming updates collapse to their final content via keyedRenderer.
type plainSink struct {
	w      io.Writer
	events *keyedRenderer
}

func newPlainSink(w io.Writer) *plainSink {
	return &plainSink{w: w, events: newKeyedRenderer(w)}
}

func (p *plainSink) Progress(msg string) {
	p.events.Flush()
	styles := theme.Default()
	style := styles.Accent
	if strings.HasPrefix(msg, "warning:") || strings.HasPrefix(msg, "note:") {
		style = styles.Warn
	}
	if strings.Contains(msg, "failed") || strings.Contains(msg, "refused") {
		style = styles.Err
	}
	fmt.Fprintln(p.w, tui.Wrap(style.Render("› "+msg), tui.TerminalWidth()))
}

func (p *plainSink) Event(ev harness.Event) { p.events.Event(ev) }

func (p *plainSink) Raw(line string) {
	p.events.Flush()
	if strings.TrimSpace(line) != "" {
		fmt.Fprintln(p.w, tui.Wrap(theme.Default().Muted.Render("  "+line), tui.TerminalWidth()))
	}
}

func (plainSink) Done() {}
