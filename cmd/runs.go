package cmd

import (
	"charm.land/lipgloss/v2"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/traust-security/traust-cli/internal/runner"
	"github.com/traust-security/traust-cli/internal/theme"
	"github.com/traust-security/traust-cli/internal/tui"
)

// Run states.
const (
	stateRunning  = "running"
	stateDone     = "done"
	stateFailed   = "failed"
	stateNeedsIn  = "needs-input"
	stateBudget   = "budget-stop"
	stateAborted  = "aborted"
	stateOrphaned = "orphaned"
)

// RunInfo is the resolved state of one run directory.
type RunInfo struct {
	ID     string `json:"id"`
	Dir    string `json:"dir"`
	Skill  string `json:"skill"`
	State  string `json:"state"`
	Exit   *int   `json:"exit_code,omitempty"`
	PID    int    `json:"pid,omitempty"`
	Tokens struct {
		In  int64 `json:"in"`
		Out int64 `json:"out"`
	} `json:"tokens"`
	StartedAt time.Time `json:"started_at"`
}

// listRuns resolves every run directory under the workspace, newest first.
func listRuns(runsDir string) ([]RunInfo, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var infos []RunInfo
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := loadRunInfo(filepath.Join(runsDir, e.Name()))
		if err != nil || info == nil {
			continue
		}
		infos = append(infos, *info)
	}
	// Running first, then by start time descending.
	sort.SliceStable(infos, func(i, j int) bool {
		ri, rj := infos[i].State == stateRunning, infos[j].State == stateRunning
		if ri != rj {
			return ri
		}
		return infos[i].StartedAt.After(infos[j].StartedAt)
	})
	return infos, nil
}

// loadRunInfo resolves one run directory's state from its files: result.json
// (terminal states), the pid file plus liveness (running), neither (orphaned
// — the worker died without writing a result, e.g. SIGKILL).
func loadRunInfo(dir string) (*RunInfo, error) {
	id := filepath.Base(dir)
	info := &RunInfo{ID: id, Dir: dir, Skill: unknownSkill(dir)}

	// spec.json carries the skill and start time.
	if data, err := os.ReadFile(filepath.Join(dir, "spec.json")); err == nil {
		var spec runner.Spec
		if json.Unmarshal(data, &spec) == nil {
			info.Skill = spec.Skill
			if t, err := time.Parse(time.RFC3339, spec.StartedAt); err == nil {
				info.StartedAt = t
			}
		}
	}

	// result.json decides terminal states.
	if data, err := os.ReadFile(filepath.Join(dir, "result.json")); err == nil {
		var res runner.RunResult
		if json.Unmarshal(data, &res) == nil {
			info.Tokens.In = res.Usage.TokensIn
			info.Tokens.Out = res.Usage.TokensOut
			exit := res.ExitCode
			info.Exit = &exit
			switch {
			case res.BudgetRefused:
				info.State = stateBudget
			case res.NeedsInput:
				info.State = stateNeedsIn
			case res.Aborted:
				info.State = stateAborted
			case res.ExitCode == 0:
				info.State = stateDone
			default:
				info.State = stateFailed
			}
			return info, nil
		}
	}

	// No result: running or orphaned.
	if pidBytes, err := os.ReadFile(filepath.Join(dir, "pid")); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes))); err == nil {
			info.PID = pid
			if processAlive(pid) {
				info.State = stateRunning
				return info, nil
			}
		}
	}
	info.State = stateOrphaned
	if info.StartedAt.IsZero() {
		// options.json's mtime anchors orphaned runs that never wrote spec.
		if fi, err := os.Stat(filepath.Join(dir, "options.json")); err == nil {
			info.StartedAt = fi.ModTime()
		}
	}
	return info, nil
}

func unknownSkill(dir string) string {
	if data, err := os.ReadFile(filepath.Join(dir, "options.json")); err == nil {
		var req runRequest
		if json.Unmarshal(data, &req) == nil {
			return req.Skill
		}
	}
	return "?"
}

// resolveRun finds the unique run matching an id prefix (or the full id).
func resolveRun(runsDir, prefix string) (*RunInfo, error) {
	infos, err := listRuns(runsDir)
	if err != nil {
		return nil, err
	}
	var matches []*RunInfo
	for i := range infos {
		if infos[i].ID == prefix || strings.HasPrefix(infos[i].ID, prefix) || shortRunID(infos[i].ID) == prefix {
			matches = append(matches, &infos[i])
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, fmt.Errorf("no run matches %q — see `traust-cli runs`", prefix)
	default:
		var ids []string
		for _, m := range matches {
			ids = append(ids, m.ID)
		}
		return nil, fmt.Errorf("ambiguous run prefix %q matches: %s", prefix, strings.Join(ids, ", "))
	}
}

var runsJSON bool

var runsCmd = &cobra.Command{
	Use:     "runs",
	Aliases: []string{"ls", "ps"},
	Short:   "List runs (running first, then newest)",
	Run: func(cmd *cobra.Command, args []string) {
		state, err := loadState()
		if err != nil {
			fatalf(1, "%v", err)
		}
		infos, err := listRuns(state.RunsDir())
		if err != nil {
			fatalf(1, "%v", err)
		}
		if runsJSON {
			out, _ := json.MarshalIndent(infos, "", "  ")
			lipgloss.Fprintln(os.Stdout, string(out))
			return
		}
		if len(infos) == 0 {
			lipgloss.Fprintln(os.Stdout, theme.Default().Muted.Render("no runs yet — start one: traust-cli run <skill> [args…]"))
			return
		}
		styles := theme.Default()
		lipgloss.Fprintf(os.Stdout, "  %-14s %-24s %-12s %-10s %s\n", styles.Label.Render("RUN"), styles.Label.Render("SKILL"), styles.Label.Render("STATE"), styles.Label.Render("AGE"), styles.Label.Render("TOKENS"))
		for _, r := range infos {
			stateStyle := styles.OK
			switch r.State {
			case stateFailed, stateOrphaned:
				stateStyle = styles.Err
			case stateRunning:
				stateStyle = styles.Accent
			case stateAborted, stateBudget, stateNeedsIn:
				stateStyle = styles.Warn
			}
			age := "—"
			if !r.StartedAt.IsZero() {
				age = humanAge(time.Since(r.StartedAt))
			}
			tokens := "—"
			if r.Tokens.In+r.Tokens.Out > 0 {
				tokens = fmt.Sprintf("in %dk out %dk", r.Tokens.In/1000, r.Tokens.Out/1000)
			}
			lipgloss.Fprintf(os.Stdout, "  %-14s %-24s %-12s %-10s %s\n",
				tui.Truncate(shortRunID(r.ID), 14, "…"),
				tui.Truncate(r.Skill, 24, "…"),
				stateStyle.Render(r.State), age, tokens)
		}
	},
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

func init() {
	rootCmd.AddCommand(runsCmd)
	runsCmd.Flags().BoolVar(&runsJSON, "json", false, "machine-readable listing")
}
