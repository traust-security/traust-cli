package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, dir, frontmatter, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\n" + frontmatter + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	h := filepath.Join(root, "harnessing")
	writeSkill(t, filepath.Join(h, "3-audit", "secure-code-audit"),
		`name: secure-code-audit
description: Multi-framework audit
argument-hint: <path-or-url>
allowed-tools:
  - Read
  - Bash(git clone:*)
metadata:
  harness:
    tier: primary`,
		"Audit the target at $ARGUMENTS.")
	writeSkill(t, filepath.Join(h, "4-triage", "triage"),
		`name: triage
description: Adversarial verification
metadata:
  harness:
    tier: primary`,
		"Verify.")
	writeSkill(t, filepath.Join(h, "census"),
		`name: census
description: Denominator authority
metadata:
  harness:
    tier: secondary`,
		"Count.")
	if err := os.MkdirAll(filepath.Join(h, "not-a-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h, "not-a-skill", "README.md"), []byte("no skill here"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadTwoLevelWalk(t *testing.T) {
	cat, err := Load(testTree(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(cat.Skills()); got != 3 {
		t.Fatalf("want 3 skills, got %d", got)
	}

	s, ok := cat.ByName("secure-code-audit")
	if !ok {
		t.Fatal("secure-code-audit not found")
	}
	if s.Stage != "3-audit" || s.StageNum != 3 {
		t.Errorf("stage = %q (%d), want 3-audit (3)", s.Stage, s.StageNum)
	}
	if s.Tier != "primary" {
		t.Errorf("tier = %q, want primary", s.Tier)
	}
	if s.ArgumentHint != "<path-or-url>" {
		t.Errorf("argument-hint = %q", s.ArgumentHint)
	}
	if len(s.AllowedTools) != 2 || s.AllowedTools[1] != "Bash(git clone:*)" {
		t.Errorf("allowed-tools = %v", s.AllowedTools)
	}
	if want := "Audit the target at $ARGUMENTS."; s.Body != want {
		t.Errorf("body = %q, want %q", s.Body, want)
	}
	if s.Standalone == nil || s.Standalone.Role != "deep-audit" || s.Standalone.MinModelClass != "mythos" {
		t.Errorf("standalone profile not merged: %+v", s.Standalone)
	}

	if s, ok := cat.ByName("census"); !ok || s.Stage != "" || s.StageNum != 0 {
		t.Errorf("root-level skill census wrong: %+v", s)
	} else if s.Standalone != nil {
		t.Errorf("census should not be in the standalone map")
	}
}

func TestStandaloneSkills(t *testing.T) {
	cat, err := Load(testTree(t))
	if err != nil {
		t.Fatal(err)
	}
	standalone := cat.StandaloneSkills()
	// secure-code-audit and triage are in the embedded map; census is not.
	if len(standalone) != 2 {
		t.Fatalf("want 2 standalone skills, got %d", len(standalone))
	}
}

func TestCheckDrift(t *testing.T) {
	cat, err := Load(testTree(t))
	if err != nil {
		t.Fatal(err)
	}
	rep := CheckDrift(cat)
	if len(rep.Missing) == 0 {
		t.Fatal("expected drift: most embedded map entries have no skill in the fixture tree")
	}
	found := false
	for _, m := range rep.Missing {
		if m == "threat-model" {
			found = true
		}
	}
	if !found {
		t.Errorf("threat-model should be reported missing, got %v", rep.Missing)
	}
}

func TestStageNum(t *testing.T) {
	cases := map[string]int{"1-inventory": 1, "9-deliver": 9, "census": 0, "x-1-weird": 0}
	for name, want := range cases {
		if got := stageNum(name); got != want {
			t.Errorf("stageNum(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestBackportFixCarriesThePatchAuthorRole(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "harnessing", "7-remediate", "backport-fix"),
		`name: backport-fix
description: Cross-release fix propagation`,
		"Backport.")
	cat, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := cat.ByName("backport-fix")
	if !ok {
		t.Fatal("backport-fix not found")
	}
	if s.Standalone == nil || s.Standalone.Role != "patch-author" || s.Standalone.MinModelClass != "mythos" {
		t.Errorf("backport-fix profile = %+v, want role patch-author, min mythos", s.Standalone)
	}
}
