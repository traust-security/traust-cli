package tui

import (
	"strings"
	"testing"

	"github.com/traust-security/traust-cli/internal/views"
)

// The shared severity enum spells the lowest level "informational": it ranks after low and
// never with critical, and an unknown value ranks with unrated rather than first.
func TestInformationalRanksBelowLowAndUnknownLast(t *testing.T) {
	rep := &views.Report{Findings: []views.Finding{
		{ID: "I", Severity: "informational"},
		{ID: "X", Severity: "bogus"},
		{ID: "C", Severity: "critical"},
		{ID: "L", Severity: "low"},
		{ID: "U"},
	}}
	m := newReportModel(rep, 120, 40)
	var got []string
	for _, f := range m.sorted {
		got = append(got, f.ID)
	}
	want := []string{"C", "L", "I", "X", "U"}
	if len(got) != len(want) {
		t.Fatalf("sorted %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted %v, want %v", got, want)
		}
	}
}

// The count header counts severities case-insensitively.
func TestCountHeaderCountsMixedCaseInformational(t *testing.T) {
	rep := &views.Report{Findings: []views.Finding{
		{ID: "A", Severity: "Informational"},
		{ID: "B", Severity: "informational"},
		{ID: "C", Severity: "HIGH"},
	}}
	out := newReportModel(rep, 120, 40).View().Content
	for _, want := range []string{"informational 2", "high 1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("header lacks %q:\n%s", want, out)
		}
	}
}
