package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/traust-security/traust-cli/internal/api"
	"github.com/traust-security/traust-cli/internal/bootstrap"
	"github.com/traust-security/traust-cli/internal/catalog"
	"github.com/traust-security/traust-cli/internal/harness"
)

// fakeAPI records every delegated engine call.
type fakeAPI struct {
	resolveModel  string
	resolveErr    error
	validateExit  int
	toolchainExit int
	toolchainHits []string
	validated     []string
	rendered      []string
	linted        []string
	spendRows     []api.SpendRow
}

func (f *fakeAPI) ReportingValidate(_ context.Context, path string, _ bool) (api.Result, error) {
	f.validated = append(f.validated, path)
	return api.Result{ExitCode: f.validateExit, Stdout: "valid: " + path}, nil
}

func (f *fakeAPI) ReportingLint(_ context.Context, paths []string, _ bool) (api.Result, error) {
	f.linted = append(f.linted, paths...)
	return api.Result{ExitCode: 0, Stdout: "ALL PASSED"}, nil
}

func (f *fakeAPI) ReportingRender(_ context.Context, report, out string) (api.Result, error) {
	f.rendered = append(f.rendered, report)
	if err := os.WriteFile(out, []byte("# rendered\n"), 0o644); err != nil {
		return api.Result{}, err
	}
	return api.Result{ExitCode: 0}, nil
}

func (f *fakeAPI) RegistryModelsResolve(_ context.Context, role string) (string, error) {
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	if f.resolveModel == "" {
		return "floor-model-x", nil
	}
	return f.resolveModel, nil
}

func (f *fakeAPI) RegistryModelsSpend(_ context.Context, row api.SpendRow) error {
	f.spendRows = append(f.spendRows, row)
	return nil
}

func (f *fakeAPI) ToolchainDoctor(_ context.Context, profile string) (api.Result, error) {
	f.toolchainHits = append(f.toolchainHits, profile)
	return api.Result{ExitCode: f.toolchainExit, Stdout: "MISSING opengrep (not found)\nMISSING gitleaks (not found)"}, nil
}

// recSink records sink traffic.
type recSink struct {
	progress []string
	events   []harness.Event
	raws     int
	done     bool
}

func (s *recSink) Progress(msg string)    { s.progress = append(s.progress, msg) }
func (s *recSink) Event(ev harness.Event) { s.events = append(s.events, ev) }
func (s *recSink) Raw(line string)        { s.raws++ }
func (s *recSink) Done()                  { s.done = true }

func testState(t *testing.T) *bootstrap.State {
	t.Helper()
	ws := t.TempDir()
	s := bootstrap.DefaultState(ws)
	if err := os.MkdirAll(s.TraustRepo(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.ConfigHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	return s
}

func testSkill() *catalog.Skill {
	return &catalog.Skill{
		Name:         "secure-code-audit",
		Stage:        "3-audit",
		Tier:         "primary",
		Body:         "Audit the target at $ARGUMENTS. Validate the report.",
		AllowedTools: []string{"Read", "Bash(git clone:*)"},
		Standalone:   &catalog.StandaloneInfo{Role: "deep-audit", MinModelClass: "mythos", Toolchain: "secure-code-audit"},
	}
}

func fakeHarnessPath(t *testing.T, name string) string {
	t.Helper()
	// The scripts live in the repo's testdata directory.
	p := filepath.Join("..", "..", "testdata", "fake-harnesses", name)
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(abs); err != nil || fi.IsDir() {
		t.Fatalf("fake harness %s missing (checked %s)", name, abs)
	}
	return abs
}

func TestRunPipelineClaudeFake(t *testing.T) {
	t.Setenv("TRAUST_CLI_CLAUDE_BIN", fakeHarnessPath(t, "fake-claude.sh"))
	state := testState(t)
	client := &fakeAPI{}
	sink := &recSink{}

	res, err := Run(context.Background(), Options{
		State: state, API: client, Harness: &harness.Claude{},
		Skill: testSkill(), Args: []string{"https://github.com/org/repo"},
		DeclareSpend: true,
	}, sink)
	if err != nil {
		t.Fatal(err)
	}

	if res.ExitCode != ExitOK {
		t.Fatalf("exit = %d (%+v)", res.ExitCode, res)
	}
	if res.Model != "floor-model-x" {
		t.Errorf("model = %q, want the registry floor resolution", res.Model)
	}
	if res.Usage.TokensIn != 100 || res.Usage.TokensOut != 50 || res.Usage.CostUSD != 0.42 {
		t.Errorf("usage = %+v", res.Usage)
	}
	if res.ResultText != "stage complete" {
		t.Errorf("result text = %q", res.ResultText)
	}
	if len(res.Artifacts) != 1 {
		t.Fatalf("artifacts = %+v", res.Artifacts)
	}
	a := res.Artifacts[0]
	if !strings.HasSuffix(a.Path, "fixture-security-audit.json") || a.Kind != "report" || !a.Valid {
		t.Errorf("artifact = %+v", a)
	}
	if a.Rendered == "" {
		t.Errorf("artifact not rendered: %+v", a)
	}
	if len(client.validated) != 1 || len(client.rendered) != 1 {
		t.Errorf("validate/render calls = %v/%v", client.validated, client.rendered)
	}
	if len(client.spendRows) != 1 {
		t.Fatalf("spend rows = %+v", client.spendRows)
	}
	row := client.spendRows[0]
	if row.Skill != "secure-code-audit" || row.Model != "floor-model-x" || row.TokensOut != 50 || row.Repo != "repo" {
		t.Errorf("spend row = %+v", row)
	}
	if !sink.done {
		t.Error("sink.Done not called")
	}
	if len(sink.events) == 0 {
		t.Error("no events forwarded to sink")
	}

	// Journal integrity.
	for _, f := range []string{"spec.json", "stream.jsonl", "result.json", "harness-stderr.log"} {
		if _, err := os.Stat(filepath.Join(res.Dir, f)); err != nil {
			t.Errorf("journal missing %s: %v", f, err)
		}
	}
	specData, err := os.ReadFile(filepath.Join(res.Dir, "spec.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec Spec
	if err := json.Unmarshal(specData, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.RegistryRole != "deep-audit" || spec.Model != "floor-model-x" {
		t.Errorf("spec = %+v", spec)
	}
}

func TestRunPipelineCodexFake(t *testing.T) {
	t.Setenv("TRAUST_CLI_CODEX_BIN", fakeHarnessPath(t, "fake-codex.sh"))
	state := testState(t)
	client := &fakeAPI{}

	skill := testSkill()
	skill.Name = "vuln-scan"
	skill.Standalone = &catalog.StandaloneInfo{Role: "vuln-scan", MinModelClass: "opus"}

	res, err := Run(context.Background(), Options{
		State: state, API: client, Harness: &harness.Codex{},
		Skill: skill, Args: []string{"/repos/target"}, DeclareSpend: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != ExitOK {
		t.Fatalf("exit = %d (%+v)", res.ExitCode, res)
	}
	if res.Usage.TokensIn != 30 || res.Usage.TokensOut != 20 {
		t.Errorf("usage = %+v", res.Usage)
	}
	if len(res.Artifacts) != 1 || !strings.HasSuffix(res.Artifacts[0].Path, "fixture-vuln-findings.json") {
		t.Fatalf("artifacts = %+v", res.Artifacts)
	}
	if res.Model != "floor-model-x" {
		t.Errorf("model = %q", res.Model)
	}
}

func TestRunNeedsInputExitsTwo(t *testing.T) {
	t.Setenv("TRAUST_CLI_CLAUDE_BIN", fakeHarnessPath(t, "fake-claude.sh"))
	t.Setenv("FAKE_CLAUDE_MODE", "needs-input")
	state := testState(t)

	res, err := Run(context.Background(), Options{
		State: state, API: &fakeAPI{}, Harness: &harness.Claude{},
		Skill: testSkill(), DeclareSpend: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != ExitNeedsInput || !res.NeedsInput {
		t.Fatalf("exit = %d, needsInput = %v", res.ExitCode, res.NeedsInput)
	}
}

func TestRunBudgetRefuseExitsThree(t *testing.T) {
	t.Setenv("TRAUST_CLI_CLAUDE_BIN", fakeHarnessPath(t, "fake-claude.sh"))
	state := testState(t)
	// Cap of 10 output tokens; the fake harness reports 50.
	policy := "budget_policy:\n  enforcement: enforced\n  per_run_defaults:\n    default: 10\n"
	if err := os.WriteFile(filepath.Join(state.ConfigHome, "budget-policy.yaml"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), Options{
		State: state, API: &fakeAPI{}, Harness: &harness.Claude{},
		Skill: testSkill(), DeclareSpend: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != ExitBudget || !res.BudgetRefused {
		t.Fatalf("exit = %d, budgetRefused = %v", res.ExitCode, res.BudgetRefused)
	}
	if res.BudgetBand != "refuse" {
		t.Errorf("band = %q", res.BudgetBand)
	}
}

func TestRunBudgetOverrideProceeds(t *testing.T) {
	t.Setenv("TRAUST_CLI_CLAUDE_BIN", fakeHarnessPath(t, "fake-claude.sh"))
	state := testState(t)
	policy := "budget_policy:\n  per_run_defaults:\n    default: 10\n"
	if err := os.WriteFile(filepath.Join(state.ConfigHome, "budget-policy.yaml"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), Options{
		State: state, API: &fakeAPI{}, Harness: &harness.Claude{},
		Skill: testSkill(), BudgetOverrideReason: "operator approved", DeclareSpend: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != ExitOK || res.BudgetRefused {
		t.Fatalf("exit = %d, budgetRefused = %v", res.ExitCode, res.BudgetRefused)
	}
	if res.BudgetBand != "refuse" {
		t.Errorf("band should still record refuse, got %q", res.BudgetBand)
	}
}

func TestRunModelOverrideWarns(t *testing.T) {
	t.Setenv("TRAUST_CLI_CLAUDE_BIN", fakeHarnessPath(t, "fake-claude.sh"))
	state := testState(t)
	sink := &recSink{}

	res, err := Run(context.Background(), Options{
		State: state, API: &fakeAPI{}, Harness: &harness.Claude{},
		Skill: testSkill(), Model: "my-own-model", DeclareSpend: false,
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "my-own-model" {
		t.Errorf("model = %q", res.Model)
	}
	if res.Preflight.Warning == "" || !strings.Contains(res.Preflight.Warning, "override") {
		t.Errorf("expected override warning, got %q", res.Preflight.Warning)
	}
	warned := false
	for _, p := range sink.progress {
		if strings.Contains(p, "override") {
			warned = true
		}
	}
	if !warned {
		t.Error("override warning not forwarded to the sink")
	}
}

func TestInputToArgs(t *testing.T) {
	args, err := InputToArgs("secure-code-audit", []byte(`{"repo":"https://github.com/org/repo","ref":"main"}`))
	if err != nil || len(args) != 1 || args[0] != "https://github.com/org/repo" {
		t.Errorf("secure-code-audit args = %v, %v", args, err)
	}
	args, err = InputToArgs("triage", []byte(`{"repo":"r","findings_path":"/f/triage.json"}`))
	if err != nil || len(args) != 1 || args[0] != "/f/triage.json" {
		t.Errorf("triage args = %v, %v", args, err)
	}
	args, err = InputToArgs("verify-remediation", []byte(`{"repo":"r","audit_report":"a.json","patched_ref":"fix"}`))
	if err != nil || strings.Join(args, ",") != "r,a.json,fix" {
		t.Errorf("verify args = %v, %v", args, err)
	}
	if _, err := InputToArgs("unknown-skill", []byte(`{}`)); err == nil {
		t.Error("unknown skill should error")
	}
}

func TestPrimaryArtifact(t *testing.T) {
	artifacts := []Artifact{
		{Path: "/root/TRIAGE.json", Kind: "report"},
		{Path: "/root/other.json", Kind: "json"},
		{Path: "/root/repo-security-audit.json", Kind: "report"},
	}
	got := PrimaryArtifact("secure-code-audit", artifacts)
	if got == nil || got.Path != "/root/repo-security-audit.json" {
		t.Errorf("primary = %+v", got)
	}
	if PrimaryArtifact("vuln-scan", artifacts) != nil {
		t.Error("vuln-scan should have no primary here")
	}
}

// modelCheckHarness wraps the claude adapter with a model check result, to
// exercise the pre-flight availability path without a real harness.
type modelCheckHarness struct {
	*harness.Claude
	checkErr error
}

func (m modelCheckHarness) CheckModel(_ context.Context, _ string) error { return m.checkErr }

func TestRunModelCheckFailsFast(t *testing.T) {
	t.Setenv("TRAUST_CLI_CLAUDE_BIN", fakeHarnessPath(t, "fake-claude.sh"))
	state := testState(t)
	client := &fakeAPI{}
	sink := &recSink{}

	res, err := Run(context.Background(), Options{
		State: state, API: client, Harness: modelCheckHarness{
			&harness.Claude{}, fmt.Errorf("model %q is not in `crush models`", "claude-mythos-5"),
		},
		Skill: testSkill(), DeclareSpend: true,
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != ExitError {
		t.Fatalf("exit = %d, want 1", res.ExitCode)
	}
	if res.Artifacts != nil || res.SpendRecorded || res.HarnessExit != 0 {
		t.Errorf("harness should never have launched: %+v", res)
	}
	if !strings.Contains(res.HarnessError, "not runnable") || !strings.Contains(res.HarnessError, "model-registry.yaml") {
		t.Errorf("harness error lacks guidance: %q", res.HarnessError)
	}
	warned := false
	for _, p := range sink.progress {
		if strings.Contains(p, "not runnable") {
			warned = true
		}
	}
	if !warned {
		t.Error("model check failure not surfaced to the sink")
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "result.json")); err != nil {
		t.Errorf("failed pre-flight should still journal: %v", err)
	}
}

func TestRunHarnessErrorSurfacesStderrTail(t *testing.T) {
	t.Setenv("TRAUST_CLI_CLAUDE_BIN", fakeHarnessPath(t, "fake-claude.sh"))
	t.Setenv("FAKE_CLAUDE_MODE", "fail")
	state := testState(t)
	sink := &recSink{}

	res, err := Run(context.Background(), Options{
		State: state, API: &fakeAPI{}, Harness: &harness.Claude{},
		Skill: testSkill(), DeclareSpend: true,
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != ExitError || res.HarnessExit != 3 {
		t.Fatalf("exit = %d, harness exit = %d", res.ExitCode, res.HarnessExit)
	}
	if !strings.Contains(res.HarnessError, "401 Unauthorized") {
		t.Errorf("harness error should carry the stderr tail: %q", res.HarnessError)
	}
	surfaced := false
	for _, p := range sink.progress {
		if strings.Contains(p, "401 Unauthorized") {
			surfaced = true
		}
	}
	if !surfaced {
		t.Error("provider error not surfaced to the sink")
	}
}

func TestRunFailedRunStillDiscoversArtifacts(t *testing.T) {
	t.Setenv("TRAUST_CLI_CLAUDE_BIN", fakeHarnessPath(t, "fake-claude.sh"))
	t.Setenv("FAKE_CLAUDE_MODE", "partial")
	state := testState(t)

	res, err := Run(context.Background(), Options{
		State: state, API: &fakeAPI{}, Harness: &harness.Claude{},
		Skill: testSkill(), DeclareSpend: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != ExitError || res.HarnessExit != 1 {
		t.Fatalf("exit = %d, harness exit = %d", res.ExitCode, res.HarnessExit)
	}
	if len(res.Artifacts) != 1 || !strings.HasSuffix(res.Artifacts[0].Path, "fixture-security-audit.json") {
		t.Fatalf("partial output should still be discovered: %+v", res.Artifacts)
	}
	if !res.Artifacts[0].Valid {
		t.Errorf("partial artifact should still validate: %+v", res.Artifacts[0])
	}
}

// TestRunToolchainPreflightWarns verifies a skill with a toolchain profile
// gets a doctor check whose failure surfaces as a NON-fatal warning naming
// the fix.
func TestRunToolchainPreflightWarns(t *testing.T) {
	t.Setenv("TRAUST_CLI_CLAUDE_BIN", fakeHarnessPath(t, "fake-claude.sh"))
	state := testState(t)
	client := &fakeAPI{toolchainExit: 1}
	sink := &recSink{}

	res, err := Run(context.Background(), Options{
		State: state, API: client, Harness: &harness.Claude{},
		Skill: testSkill(), DeclareSpend: false,
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	// secure-code-audit's profile was checked…
	if len(client.toolchainHits) != 1 || client.toolchainHits[0] != "secure-code-audit" {
		t.Errorf("toolchain doctor calls = %v", client.toolchainHits)
	}
	// …the warning names the profile and the fix…
	warned := false
	for _, p := range sink.progress {
		if strings.Contains(p, `toolchain profile "secure-code-audit"`) && strings.Contains(p, "reduced scanner coverage") && strings.Contains(p, "traust-cli init") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("toolchain warning not surfaced: %v", sink.progress)
	}
	// …and the run still completes (degraded, not gated).
	if res.ExitCode != ExitOK {
		t.Fatalf("exit = %d — toolchain preflight must be a warning, not a gate", res.ExitCode)
	}
}

// TestRunToolchainPreflightSkipsCleanProfiles verifies a healthy profile
// produces no warning.
func TestRunToolchainPreflightSkipsCleanProfiles(t *testing.T) {
	t.Setenv("TRAUST_CLI_CLAUDE_BIN", fakeHarnessPath(t, "fake-claude.sh"))
	state := testState(t)
	client := &fakeAPI{toolchainExit: 0}
	sink := &recSink{}

	_, err := Run(context.Background(), Options{
		State: state, API: client, Harness: &harness.Claude{},
		Skill: testSkill(), DeclareSpend: false,
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range sink.progress {
		if strings.Contains(p, "toolchain profile") {
			t.Errorf("clean profile should not warn: %q", p)
		}
	}
}

// TestAuthFailureGuidance is the regression test for the silent-401 class
// of failure: an authentication error must carry actionable guidance
// naming the resolved model, the registry fix, and flag placement.
func TestAuthFailureGuidance(t *testing.T) {
	t.Setenv("TRAUST_CLI_CLAUDE_BIN", fakeHarnessPath(t, "fake-claude.sh"))
	t.Setenv("FAKE_CLAUDE_MODE", "fail-auth")
	state := testState(t)
	sink := &recSink{}

	res, err := Run(context.Background(), Options{
		State: state, API: &fakeAPI{}, Harness: &harness.Claude{},
		Skill: testSkill(), DeclareSpend: false,
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != ExitError {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	for _, want := range []string{"authentication failed for model", "model-registry.yaml", "--model BEFORE the skill name"} {
		if !strings.Contains(res.HarnessError, want) {
			t.Errorf("harness error missing %q: %q", want, res.HarnessError)
		}
	}
	noted := false
	for _, p := range sink.progress {
		if strings.Contains(p, "authentication failed for model") {
			noted = true
		}
	}
	if !noted {
		t.Error("guidance not surfaced to the sink")
	}
}

func TestPreflightNoRole(t *testing.T) {
	skill := testSkill()
	skill.Standalone = &catalog.StandaloneInfo{MinModelClass: "opus"}
	pf := ResolveModel(context.Background(), &fakeAPI{}, skill, "")
	if pf.Model != "" || pf.Warning == "" {
		t.Errorf("preflight = %+v", pf)
	}
}

func TestPreflightResolvesTheBackportFixRole(t *testing.T) {
	skill := testSkill()
	skill.Name = "backport-fix"
	skill.Standalone = &catalog.StandaloneInfo{Role: "patch-author", MinModelClass: "mythos"}
	pf := ResolveModel(context.Background(), &fakeAPI{resolveModel: "patch-floor"}, skill, "")
	if pf.Role != "patch-author" || pf.Model != "patch-floor" || pf.Warning != "" {
		t.Errorf("preflight = %+v, want the patch-author floor", pf)
	}
}
