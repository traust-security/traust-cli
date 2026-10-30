package cmd

import (
	"charm.land/lipgloss/v2"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/traust-security/traust-cli/internal/harness"
	"github.com/traust-security/traust-cli/internal/theme"
)

var flowFlags struct {
	harnessID            string
	model                string
	unsafeFullPermission bool
	budgetOverride       string
	detach               bool
}

var flowCmd = &cobra.Command{
	Use:   "flow",
	Short: "Multi-stage pipelines: audit → triage → patch → verify",
	Long: `Chained skill pipelines. Every stage runs as an ordinary background
run (its own journal, result and attach session), with each stage's inputs
derived from the previous stage's artifacts:

  audit-triage <repo>            secure-code-audit, then triage (--auto) of
                                 its report. Output: the triaged findings
                                 (<repo>-triage.json) — the hand-off point.

  remediate-verify <findings> <repo>
                                 patch (fix generation) from triaged
                                 findings or an audit report, then
                                 verify-remediation against a patched
                                 checkout with the patches applied.

  full <repo>                    all four stages end to end.

Ctrl-C detaches without cancelling; the flow keeps going in its own
process. ` + "`flow status`" + ` lists flows, ` + "`flow status <id>`" + ` shows
stage detail, and ` + "`attach <id>`" + ` attaches to the current stage.`,
}

var flowAuditTriageCmd = &cobra.Command{
	Use:   "audit-triage <repo>",
	Short: "Audit a repository, then triage the findings",
	Args:  cobra.ExactArgs(1),
	Run:   func(cmd *cobra.Command, args []string) { startFlowCmd("audit-triage", args[0], "") },
}

var flowRemediateVerifyCmd = &cobra.Command{
	Use:   "remediate-verify <findings-path> <repo>",
	Short: "Patch triaged findings, then verify the remediations",
	Long: `Consumes the output of ` + "`flow audit-triage`" + ` (a <repo>-triage.json,
or a <repo>-security-audit.json) and the target repository: generates
fixes (PATCHES/), applies them to a fresh checkout, and verifies each
original finding against the patched code.`,
	Args: cobra.ExactArgs(2),
	Run:  func(cmd *cobra.Command, args []string) { startFlowCmd("remediate-verify", args[1], args[0]) },
}

var flowFullCmd = &cobra.Command{
	Use:   "full <repo>",
	Short: "The whole loop: audit, triage, patch, verify",
	Args:  cobra.ExactArgs(1),
	Run:   func(cmd *cobra.Command, args []string) { startFlowCmd("full", args[0], "") },
}

var flowStatusCmd = &cobra.Command{
	Use:   "status [flow]",
	Short: "List flows, or show one flow's stages",
	Args:  cobra.MaximumNArgs(1),
	Run:   runFlowStatus,
}

func init() {
	rootCmd.AddCommand(flowCmd)
	flowCmd.AddCommand(flowAuditTriageCmd, flowRemediateVerifyCmd, flowFullCmd, flowStatusCmd)
	for _, c := range []*cobra.Command{flowAuditTriageCmd, flowRemediateVerifyCmd, flowFullCmd} {
		c.Flags().StringVar(&flowFlags.harnessID, "harness", "", "agent harness for every stage (default: state default)")
		c.Flags().StringVar(&flowFlags.model, "model", "", "model override for every stage (default: per-stage registry floor)")
		c.Flags().BoolVar(&flowFlags.unsafeFullPermission, "unsafe-full-permission", false, "grant the harness full permission for every stage (also settable at init)")
		c.Flags().StringVar(&flowFlags.budgetOverride, "budget-override", "", "reason to proceed past budget refuse bands (recorded)")
		c.Flags().BoolVarP(&flowFlags.detach, "detach", "d", false, "start without attaching (interactive flows attach by default)")
	}
}

func startFlowCmd(flow, target, findingsPath string) {
	state, err := loadState()
	if err != nil {
		fatalf(1, "%v", err)
	}

	harnessID := flowFlags.harnessID
	if harnessID == "" {
		harnessID = state.Harness
	}
	h, err := harness.Get(harnessID)
	if err != nil {
		fatalf(1, "%v", err)
	}
	if d := h.Detect(context.Background()); !d.Available {
		fatalf(1, "%s binary %q not found — install it or pick another harness with --harness", h.DisplayName(), h.Binary())
	}

	if findingsPath != "" {
		if _, err := os.Stat(findingsPath); err != nil {
			fatalf(1, "findings path %q: %v", findingsPath, err)
		}
	}

	unsafe := flowFlags.unsafeFullPermission || state.FullPermission
	id, err := startFlow(state, flowRequest{
		Flow:                 flow,
		Target:               target,
		FindingsPath:         findingsPath,
		Harness:              h.ID(),
		Model:                flowFlags.model,
		UnsafeFullPermission: unsafe,
		BudgetOverride:       flowFlags.budgetOverride,
	})
	if err != nil {
		fatalf(1, "start flow: %v", err)
	}

	short := shortFlowID(id)
	if flowFlags.detach || !isTTY() {
		printFlowStarted(id, short)
		return
	}
	// Interactive: follow the flow stage by stage (attaching to whichever
	// stage is current) until it ends or the user detaches.
	attachFlow(id)
}

// attachFlow follows a pipeline: attach to the current stage's run; when
// that stage completes, roll on to the next. Detaching (ctrl+c) during a
// stage leaves the flow running and stops following.
func attachFlow(flowID string) {
	state, err := loadState()
	if err != nil {
		fatalf(1, "%v", err)
	}
	dir := filepath.Join(flowsDir(state.Workspace), flowID)
	runsDir := state.RunsDir()
	styles := theme.Default()

	for {
		m, err := loadFlowManifest(dir)
		if err != nil {
			fatalf(1, "flow vanished: %v", err)
		}
		if m.State != stateRunning {
			printFlowManifest(m)
			lipgloss.Fprintln(os.Stdout)
			if m.State == stateFailed {
				os.Exit(1)
			}
			return
		}
		stage := m.currentStage()
		if stage == nil || stage.RunID == "" || stage.State != stateRunning {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		info, err := loadRunInfo(filepath.Join(runsDir, stage.RunID))
		if err != nil || info == nil || info.State != stateRunning {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		lipgloss.Fprintln(os.Stdout, styles.Accent.Render(fmt.Sprintf("▸ flow stage: %s (run %s)", stage.Name, shortRunID(stage.RunID))))
		attachOnce(info)
		if !resultExists(info.Dir) {
			// The stage is still going: the user detached.
			lipgloss.Fprintln(os.Stdout)
			lipgloss.Fprintln(os.Stdout, styles.OK.Render("▸ detached — the flow continues in the background"))
			kv("status", "traust-cli flow status "+shortFlowID(flowID))
			kv("watch", "traust-cli attach "+shortFlowID(flowID))
			return
		}
		// Stage finished; roll on to the next.
	}
}

func printFlowStarted(id, short string) {
	styles := theme.Default()
	lipgloss.Fprintln(os.Stdout)
	lipgloss.Fprintln(os.Stdout, styles.OK.Render("▸ flow "+id+" started in the background"))
	kv("status", "traust-cli flow status "+short)
	kv("watch", "traust-cli attach "+short)
	kv("stages", "traust-cli runs")
}

func shortFlowID(id string) string { return shortRunID(id) }

func runFlowStatus(cmd *cobra.Command, args []string) {
	state, err := loadState()
	if err != nil {
		fatalf(1, "%v", err)
	}
	dir := flowsDir(state.Workspace)
	if len(args) == 1 {
		m := resolveFlow(dir, args[0])
		printFlowManifest(m)
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			lipgloss.Fprintln(os.Stdout, theme.Default().Muted.Render("no flows yet — start one: traust-cli flow full <repo>"))
			return
		}
		fatalf(1, "%v", err)
	}
	var manifests []*flowManifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if m, err := loadFlowManifest(filepath.Join(dir, e.Name())); err == nil {
			manifests = append(manifests, m)
		}
	}
	sort.Slice(manifests, func(i, j int) bool {
		ri, rj := manifests[i].State == stateRunning, manifests[j].State == stateRunning
		if ri != rj {
			return ri
		}
		return manifests[i].CreatedAt > manifests[j].CreatedAt
	})
	styles := theme.Default()
	lipgloss.Fprintf(os.Stdout, "  %-14s %-16s %-10s %s\n", styles.Label.Render("FLOW"), styles.Label.Render("KIND"), styles.Label.Render("STATE"), styles.Label.Render("TARGET"))
	for _, m := range manifests {
		stateStyle := styles.OK
		switch m.State {
		case stateFailed:
			stateStyle = styles.Err
		case stateRunning:
			stateStyle = styles.Accent
		}
		lipgloss.Fprintf(os.Stdout, "  %-14s %-16s %-10s %s\n", shortFlowID(m.ID), m.Flow, stateStyle.Render(m.State), m.Target)
	}
}

func resolveFlow(dir, prefix string) *flowManifest {
	m, err := resolveFlowE(dir, prefix)
	if err != nil {
		fatalf(1, "%v", err)
	}
	return m
}

// resolveFlowE resolves a flow by id prefix without exiting.
func resolveFlowE(dir, prefix string) (*flowManifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("no flows: %w", err)
	}
	var matches []*flowManifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if e.Name() == prefix || hasSuffixID(e.Name(), prefix) {
			if m, err := loadFlowManifest(filepath.Join(dir, e.Name())); err == nil {
				matches = append(matches, m)
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, fmt.Errorf("no flow matches %q", prefix)
	default:
		return nil, fmt.Errorf("ambiguous flow prefix %q", prefix)
	}
}

func hasSuffixID(name, prefix string) bool {
	return len(name) > len(prefix) && shortRunID(name) == prefix
}

func printFlowManifest(m *flowManifest) {
	styles := theme.Default()
	lipgloss.Fprintln(os.Stdout)
	lipgloss.Fprintln(os.Stdout, styles.Header.Render("flow "+m.ID))
	kv("kind", m.Flow)
	kv("target", m.Target)
	kv("state", m.State)
	lipgloss.Fprintln(os.Stdout)
	for _, s := range m.Stages {
		mark := styles.OK.Render("✓")
		stateStyle := styles.OK
		switch s.State {
		case stateFailed:
			mark = styles.Err.Render("✗")
			stateStyle = styles.Err
		case stateRunning:
			mark = styles.Accent.Render("▸")
			stateStyle = styles.Accent
		case "pending":
			mark = styles.Muted.Render("·")
			stateStyle = styles.Muted
		case "skipped":
			mark = styles.Muted.Render("-")
			stateStyle = styles.Muted
		}
		line := fmt.Sprintf("  %s %-10s %-16s %s", mark, s.Name, stateStyle.Render(s.State), s.Skill)
		if s.RunID != "" {
			line += styles.Muted.Render("  (run " + shortRunID(s.RunID) + ")")
		}
		lipgloss.Fprintln(os.Stdout, line)
		if s.Detail != "" {
			lipgloss.Fprintln(os.Stdout, styles.Muted.Render("      "+s.Detail))
		}
		if s.Artifact != "" && s.State == stateDone {
			lipgloss.Fprintln(os.Stdout, styles.Muted.Render("      artifact: "+s.Artifact))
		}
	}
	if cur := m.currentStage(); cur != nil && cur.RunID != "" && cur.State == stateRunning {
		lipgloss.Fprintln(os.Stdout)
		lipgloss.Fprintln(os.Stdout, styles.Muted.Render("watch: traust-cli attach "+shortRunID(cur.RunID)))
	}
}
