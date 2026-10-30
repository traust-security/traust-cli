package cmd

import (
	"charm.land/lipgloss/v2"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/traust-security/traust-cli/internal/bootstrap"
	"github.com/traust-security/traust-cli/internal/theme"
)

// runRequest is the parent's serialized instruction for the worker: it is
// everything needed to reconstruct the run after the launching terminal is
// gone. Stored as options.json in the run directory.
type runRequest struct {
	RunID                string   `json:"run_id"`
	Workspace            string   `json:"workspace"`
	Skill                string   `json:"skill"`
	Args                 []string `json:"args"`
	Harness              string   `json:"harness"`
	Model                string   `json:"model,omitempty"`
	MaxTurns             int      `json:"max_turns,omitempty"`
	UnsafeFullPermission bool     `json:"unsafe_full_permission,omitempty"`
	BudgetOverride       string   `json:"budget_override,omitempty"`
	StrictValidation     bool     `json:"strict_validation,omitempty"`
	DeclareSpend         bool     `json:"declare_spend"`
}

// newRunID mints a collision-safe run id: timestamp + skill + random suffix.
// The suffix is 3 bytes (6 hex chars): a 2-byte suffix collides ~8% of the
// time once a dozen runs share a second (birthday paradox), and a collision
// means a shared run directory.
func newRunID(skill string) (string, error) {
	buf := make([]byte, 3)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%s", time.Now().Format("20060102-150405"), skill, hex.EncodeToString(buf)), nil
}

// startBackgroundRun launches a detached worker for the run and returns once
// the worker has taken ownership (pid file present). The worker survives
// this process and the terminal it was launched from.
func startBackgroundRun(state *bootstrap.State, req runRequest) (string, error) {
	runID := req.RunID
	dir := filepath.Join(state.RunsDir(), runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create run dir: %w", err)
	}
	req.Workspace = state.Workspace
	req.DeclareSpend = true
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "options.json"), data, 0o644); err != nil {
		return "", err
	}

	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	workerLog, err := os.Create(filepath.Join(dir, "worker.log"))
	if err != nil {
		return "", err
	}
	defer workerLog.Close()

	cmd := exec.Command(exe, "_worker", dir)
	cmd.SysProcAttr = detachedSysProcAttr()
	cmd.Env = os.Environ()
	cmd.Stdout = workerLog
	cmd.Stderr = workerLog
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start worker: %w", err)
	}
	// Reap the worker when it eventually exits; the parent never blocks on
	// it, and an unreaped child would read as alive to the pid probe below.
	go func() { _ = cmd.Wait() }()

	// Handshake: wait for the worker's pid file — or, for runs that finish
	// almost instantly, its result.json. The worker writes the pid before
	// loading anything and removes it at exit, so a completed run can beat
	// the parent's first poll.
	pidPath := filepath.Join(dir, "pid")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pidPath); err == nil {
			return runID, nil
		}
		if _, err := os.Stat(filepath.Join(dir, "result.json")); err == nil {
			return runID, nil
		}
		if !processAlive(cmd.Process.Pid) {
			return "", fmt.Errorf("worker exited during startup — see %s", filepath.Join(dir, "worker.log"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", fmt.Errorf("worker did not start within 15s — see %s", filepath.Join(dir, "worker.log"))
}

// printStarted shows the background-run affordances.
func printStarted(runID string) {
	styles := theme.Default()
	short := shortRunID(runID)
	lipgloss.Fprintln(os.Stdout)
	lipgloss.Fprintln(os.Stdout, styles.OK.Render("▸ run "+runID+" started in the background"))
	kv("watch", "traust-cli attach "+short)
	kv("status", "traust-cli runs")
	kv("stop", "traust-cli stop "+short)
	kv("result", "traust-cli result "+short)
}

// shortRunID returns the unambiguous short form of a run id: the random
// 4-hex suffix for background runs. Legacy ids (no suffix) come back
// unchanged — they are historical, and full-id prefix matching still works.
func shortRunID(runID string) string {
	parts := strings.Split(runID, "-")
	if len(parts) >= 4 {
		last := parts[len(parts)-1]
		if isHexSuffix(last) {
			return last
		}
	}
	return runID
}

// isHexSuffix reports whether s is a short hex string (4-6 characters) —
// the random suffix minted by newRunID, in either width.
func isHexSuffix(s string) bool {
	if len(s) < 4 || len(s) > 6 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
