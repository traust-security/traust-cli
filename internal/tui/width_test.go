package tui

import (
	"io"
	"strings"
	"testing"
	"time"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/traust-security/traust-cli/internal/views"
)

// TestMonitorUpsertStreamsInPlace verifies keyed lines update in place:
// a streaming part rewrites its line instead of appending fragments.
func TestMonitorUpsertStreamsInPlace(t *testing.T) {
	m := newMonitor(MonitorHeader{Skill: "s"}, nil, 100, 30)
	m.upsertLine("m1:0", "The tar")
	m.upsertLine("m1:0", "The target is a Go monorepo")
	m.upsertLine("m1:1", "→ bash  rg -n")
	m.upsertLine("m1:1", "→ bash  rg -n r.GET router.go")
	m.pushLine("done line")

	if got := len(m.lines); got != 3 {
		t.Fatalf("lines = %d (want 3: two streamed + one appended), contents: %v", got, m.lines)
	}
	if m.lines[0].text != "The target is a Go monorepo" {
		t.Errorf("line 0 = %q (want final streamed text)", m.lines[0].text)
	}
	if !strings.HasPrefix(m.lines[1].text, "→ bash") || !strings.Contains(m.lines[1].text, "r.GET") {
		t.Errorf("line 1 = %q (want final tool input)", m.lines[1].text)
	}
	if m.lines[2].text != "done line" || m.lines[2].key != "" {
		t.Errorf("line 2 = %+v (unkeyed appends)", m.lines[2])
	}
}

// TestAttachDetachReturnsWhileFeedOpen is the regression test for the
// detach hang: the monitor must exit when the user quits, even though the
// journal tailer keeps the feed channel open (the run continues).
func TestAttachDetachReturnsWhileFeedOpen(t *testing.T) {
	follow := make(chan MonitorItem, 8) // never closed — the run is still going

	done := make(chan error, 1)
	go func() {
		_, err := attachProgram(MonitorHeader{Skill: "s", Harness: "crush"},
			follow, nil,
			tea.WithInput(strings.NewReader("\x03")), // ctrl+c: detach
			tea.WithOutput(io.Discard),
			tea.WithoutSignalHandler())
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("attach: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("attach did not return after ctrl+c while the feed stayed open — detach hang regression")
	}
}

func TestWrapRespectsWidth(t *testing.T) {
	cases := []struct {
		width int
		text  string
	}{
		{20, strings.Repeat("x", 100)},
		{40, "short line"},
		{30, "word " + strings.Repeat("longword", 10)},
	}
	for _, c := range cases {
		wrapped := Wrap(c.text, c.width)
		for _, line := range strings.Split(wrapped, "\n") {
			if w := lipgloss.Width(line); w > c.width {
				t.Errorf("wrap(width=%d) produced a %d-cell line: %q", c.width, w, line)
			}
		}
	}
}

func TestWrapPreservesANSI(t *testing.T) {
	// The test process has no TTY: force a color profile so Render emits
	// real escapes.
	styled := lipgloss.NewStyle().Foreground(lipgloss.Color("#EE0000")).Render(strings.Repeat("red ", 20))
	wrapped := Wrap(styled, 12)
	// A hard wrapper does not re-emit styles on continuation lines; the
	// requirement is that every line fits and the escapes survive the wrap
	// without corrupting the width calculation.
	if !strings.Contains(wrapped, "\x1b[") {
		t.Error("wrap stripped all ANSI styling")
	}
	for _, line := range strings.Split(wrapped, "\n") {
		if w := lipgloss.Width(line); w > 12 {
			t.Errorf("wrapped line is %d cells wide (limit 12): %q", w, line)
		}
	}
}

func TestTruncate(t *testing.T) {
	styled := lipgloss.NewStyle().Bold(true).Render(strings.Repeat("x", 50))
	got := Truncate(styled, 10, "…")
	if w := lipgloss.Width(got); w > 10 {
		t.Errorf("truncate produced %d cells", w)
	}
	// The reset escape trails the tail, so check containment not suffix.
	if !strings.Contains(got, "…") {
		t.Errorf("truncate should keep the tail: %q", got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("truncate should preserve styling: %q", got)
	}
}

// TestMonitorRewrapsOnResize is the regression test for the width bug:
// lines pushed at one width must re-flow when the terminal resizes.
func TestMonitorRewrapsOnResize(t *testing.T) {
	m := newMonitor(MonitorHeader{Skill: "s", Harness: "claude", Model: "m", BudgetCap: 0}, nil, 100, 30)
	m.pushLine(strings.Repeat("x", 100)) // viewport width 98 → 2 display lines

	if got := m.viewport.TotalLineCount(); got != 2 {
		t.Fatalf("at width 100 the line should occupy 2 display lines, got %d", got)
	}

	// Narrow the terminal to 50 (viewport 48): the same stored line must
	// re-wrap to 3.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 50, Height: 30})
	m2, ok := updated.(monitorModel)
	if !ok {
		t.Fatalf("update returned %T", updated)
	}
	if got := m2.viewport.TotalLineCount(); got != 3 {
		t.Fatalf("at width 50 the line should re-wrap to 3 display lines, got %d", got)
	}
}

// TestMonitorHeaderFitsNarrowTerminals guards the one-line header invariant.
func TestMonitorHeaderFitsNarrowTerminals(t *testing.T) {
	for _, width := range []int{40, 60, 80, 200} {
		m := newMonitor(MonitorHeader{Skill: "secure-code-audit", Harness: "claude", Model: "rits/zai-org/glm-5-3", BudgetCap: 1000000}, nil, width, 30)
		m.tokensIn, m.tokensOut = 123456, 654321
		out := m.View()
		for _, line := range strings.Split(strings.TrimRight(out.Content, "\n"), "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("width %d: view has a %d-cell line: %q", width, w, line)
			}
		}
	}
}

// TestReportTableRebuildsOnResize guards the report browser's adaptive
// columns.
func TestReportTableRebuildsOnResize(t *testing.T) {
	rep := &views.Report{
		Path: "/tmp/x/repo-security-audit.json",
		Findings: []views.Finding{
			{ID: "F-001", Severity: "high", Title: strings.Repeat("long title ", 15)},
			{ID: "F-002", Severity: "critical", Title: "short"},
		},
	}
	m := newReportModel(rep, 120, 40)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 50, Height: 24})
	m2, ok := updated.(reportModel)
	if !ok {
		t.Fatalf("update returned %T", updated)
	}
	out := m2.View()
	for _, line := range strings.Split(strings.TrimRight(out.Content, "\n"), "\n") {
		if w := lipgloss.Width(line); w > 50 {
			t.Errorf("width 50: view has a %d-cell line: %q", w, line)
		}
	}
	// The cursor must survive the rebuild.
	if m2.table.Cursor() != 0 {
		t.Errorf("cursor = %d after rebuild", m2.table.Cursor())
	}
}
