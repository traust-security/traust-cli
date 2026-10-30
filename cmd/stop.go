package cmd

import (
	"charm.land/lipgloss/v2"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/traust-security/traust-cli/internal/theme"
	"github.com/traust-security/traust-cli/internal/tui"
)

var stopCmd = &cobra.Command{
	Use:   "stop <run>",
	Short: "Cancel a background run (graceful: SIGINT, then SIGTERM, then SIGKILL)",
	Args:  cobra.ExactArgs(1),
	Run:   runStop,
}

var logsCmd = &cobra.Command{
	Use:   "logs <run>",
	Short: "Print a run's journal (rendered transcript)",
	Args:  cobra.ExactArgs(1),
	Run:   runLogs,
}

var resultCmd = &cobra.Command{
	Use:   "result <run>",
	Short: "Print a run's stored result (summary, artifacts, usage)",
	Args:  cobra.ExactArgs(1),
	Run:   runResult,
}

func init() {
	rootCmd.AddCommand(stopCmd, logsCmd, resultCmd)
}

func runStop(cmd *cobra.Command, args []string) {
	state, err := loadState()
	if err != nil {
		fatalf(1, "%v", err)
	}
	info, err := resolveRun(state.RunsDir(), args[0])
	if err != nil {
		fatalf(1, "%v", err)
	}
	stopRun(info)
}

// stopRun cancels a background run through the signal ladder, verbosely
// (for the CLI command). The worker turns the first signal into a context
// cancellation, which tears the harness down through the graceful paths
// (crush cancel endpoint, server shutdown) and writes result.json with
// aborted=true.
func stopRun(info *RunInfo) {
	if !signalRunStop(info) {
		return
	}
	if waitForResult(info.Dir, 15*time.Second) {
		lipgloss.Fprintln(os.Stdout, theme.Default().OK.Render("▸ run cancelled — result written"))
		return
	}
	lipgloss.Fprintln(os.Stdout, theme.Default().Warn.Render("› graceful cancel did not finish — sending SIGTERM"))
	syscall.Kill(info.PID, syscall.SIGTERM)
	if waitForResult(info.Dir, 10*time.Second) {
		lipgloss.Fprintln(os.Stdout, theme.Default().OK.Render("▸ run cancelled — result written"))
		return
	}
	lipgloss.Fprintln(os.Stdout, theme.Default().Err.Render("✗ worker did not stop — sending SIGKILL (no result will be written; the run will show as orphaned)"))
	syscall.Kill(info.PID, syscall.SIGKILL)
}

// signalRunStop sends the first (graceful) stop signal; false when there is
// nothing to stop. It prints nothing — the attach monitor uses it as its
// cancel callback, where stdout belongs to the TUI.
func signalRunStop(info *RunInfo) bool {
	if info.State != stateRunning {
		return false
	}
	if !processAlive(info.PID) {
		return false
	}
	return syscall.Kill(info.PID, syscall.SIGINT) == nil
}

func waitForResult(dir string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if resultExists(dir) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func runLogs(cmd *cobra.Command, args []string) {
	state, err := loadState()
	if err != nil {
		fatalf(1, "%v", err)
	}
	info, err := resolveRun(state.RunsDir(), args[0])
	if err != nil {
		fatalf(1, "%v", err)
	}
	styles := theme.Default()
	stream := filepath.Join(info.Dir, "stream.jsonl")
	data, err := os.ReadFile(stream)
	if err != nil {
		fatalf(1, "no journal at %s: %v", stream, err)
	}
	lipgloss.Fprintln(os.Stdout, styles.Muted.Render("journal: "+stream))
	events := newKeyedRenderer(os.Stdout)
	for _, line := range splitLines(string(data)) {
		if it, ok := journalLineToItem(line); ok {
			switch it.Kind {
			case tui.ItemEvent:
				events.Event(it.Event)
			case tui.ItemRaw:
				events.Flush()
				if it.Text != "" {
					lipgloss.Fprintln(os.Stdout, tui.Wrap(styles.Muted.Render("  "+it.Text), tui.TerminalWidth()))
				}
			}
		}
	}
	events.Flush()
	if resultExists(info.Dir) {
		printResultFromDir(info.Dir)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func runResult(cmd *cobra.Command, args []string) {
	state, err := loadState()
	if err != nil {
		fatalf(1, "%v", err)
	}
	info, err := resolveRun(state.RunsDir(), args[0])
	if err != nil {
		fatalf(1, "%v", err)
	}
	if info.State == stateRunning {
		styles := theme.Default()
		lipgloss.Fprintln(os.Stdout, styles.Accent.Render(fmt.Sprintf("run %s is still running — `traust-cli attach %s` to watch it", info.ID, shortRunID(info.ID))))
		return
	}
	printResultFromDir(info.Dir)
	os.Exit(*info.Exit)
}
