package tui

import (
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
