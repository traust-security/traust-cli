package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/traust-security/traust-cli/internal/theme"
	"github.com/traust-security/traust-cli/internal/views"
)

// reportModel browses a report's findings: a severity-colored table with a
// glamour-rendered detail pane. Read-only.
//
// Everything width-dependent (table columns, markdown word wrap) is derived
// from the CURRENT width and rebuilt on every WindowSizeMsg, so resizing the
// terminal reflows the layout instead of clipping to the startup size.
type reportModel struct {
	styles   theme.Styles
	table    table.Model
	viewport viewport.Model
	report   *views.Report
	// sorted holds the severity-ordered findings; rows are derived from it
	// at the CURRENT width in buildTable, so a resize re-truncates them.
	sorted   []views.Finding
	mode     string // "table" | "detail"
	detailMD string
	renderer *glamour.TermRenderer
	width    int
	height   int
	// tableBuilt distinguishes "no table yet" from a zero-value table
	// (table.Model is a struct, not a pointer).
	tableBuilt bool
}

var severityOrder = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "info": 4, "none": 5, "unrated": 6}

func newReportModel(rep *views.Report, width, height int) reportModel {
	styles := theme.Default()
	m := reportModel{styles: styles, report: rep, mode: "table", width: width, height: height}

	sorted := make([]views.Finding, len(rep.Findings))
	copy(sorted, rep.Findings)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && severityOrder[sevKey(sorted[j])] < severityOrder[sevKey(sorted[j-1])]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	m.sorted = sorted

	m.viewport = viewport.New(viewport.WithWidth(width-4), viewport.WithHeight(height-10))
	m.buildTable()
	m.buildRenderer()
	return m
}

// buildTable (re)creates the findings table with column widths derived from
// the current terminal width, re-truncating the rows to match and preserving
// the cursor.
func (m *reportModel) buildTable() {
	sevW := 9
	idW := clampInt(m.width/4, 10, 30)
	titleW := maxInt(m.width-6-sevW-idW, 12)
	cols := []table.Column{
		{Title: "SEV", Width: sevW},
		{Title: "ID", Width: idW},
		{Title: "TITLE", Width: titleW},
	}
	rows := make([]table.Row, 0, len(m.sorted))
	for _, f := range m.sorted {
		id := f.ID
		if id == "" {
			id = "—"
		}
		rows = append(rows, table.Row([]string{
			strings.ToUpper(f.Severity),
			clip(id, idW-1),
			clip(f.Title, titleW-1),
		}))
	}
	t := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithHeight(clampInt(m.height-10, 5, 20)),
		table.WithWidth(maxInt(m.width-4, 20)),
	)
	t.SetStyles(table.Styles{
		Header:   m.styles.Header.BorderStyle(lipgloss.NormalBorder()).BorderBottom(true).BorderForeground(theme.Palette.Muted),
		Selected: lipgloss.NewStyle().Bold(true).Foreground(theme.Palette.Text).Background(theme.Palette.Surface),
	})
	if m.tableBuilt {
		t.SetCursor(m.table.Cursor())
	}
	m.table = t
	m.tableBuilt = true
}

// clip truncates a plain string to n cells.
func clip(s string, n int) string {
	if n < 1 {
		n = 1
	}
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

// buildRenderer (re)creates the glamour renderer at the current width and
// re-renders the open detail pane.
func (m *reportModel) buildRenderer() {
	r, err := glamour.NewTermRenderer(glamour.WithWordWrap(maxInt(m.width-8, 20)), glamour.WithStandardStyle("dark"))
	if err == nil {
		m.renderer = r
	}
	if m.mode == "detail" {
		m.renderDetail()
	}
}

func sevKey(f views.Finding) string {
	if f.Severity == "" {
		return "unrated"
	}
	return f.Severity
}

func (m reportModel) Init() tea.Cmd { return nil }

func (m reportModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.viewport.SetWidth(maxInt(msg.Width-4, 20))
		m.viewport.SetHeight(maxInt(msg.Height-10, 3))
		m.buildTable()
		m.buildRenderer()
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			if m.mode == "table" && len(m.report.Findings) > 0 {
				m.showDetail(m.table.Cursor())
				m.mode = "detail"
				return m, nil
			}
		case "esc":
			m.mode = "table"
			return m, nil
		}
	}
	if m.mode == "detail" {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m *reportModel) showDetail(idx int) {
	if idx < 0 || idx >= len(m.report.Findings) {
		return
	}
	f := m.report.Findings[idx]
	m.detailMD = fmt.Sprintf("# %s\n\n**Severity:** %s  ·  **ID:** %s\n\n%s\n", f.Title, f.Severity, f.ID, f.Description)
	m.renderDetail()
}

func (m *reportModel) renderDetail() {
	if m.detailMD == "" {
		return
	}
	rendered := m.detailMD
	if m.renderer != nil {
		if out, err := m.renderer.Render(m.detailMD); err == nil {
			rendered = out
		}
	}
	m.viewport.SetContent(rendered)
	m.viewport.GotoTop()
}

func (m reportModel) View() tea.View {
	var b strings.Builder
	counts := m.report.CountsBySeverity()
	var parts []string
	for _, sev := range []string{"critical", "high", "medium", "low", "info"} {
		if n := counts[sev]; n > 0 {
			parts = append(parts, theme.SeverityStyle(m.styles, sev).Render(fmt.Sprintf("%s %d", sev, n)))
		}
	}
	title := m.styles.App.Render(" traust ") + m.styles.Header.Render(" "+shortPath(m.report.Path))
	b.WriteString("\n" + Truncate(title, m.width, "…") + "\n")
	if len(parts) > 0 {
		b.WriteString(Truncate(strings.Join(parts, "  "), m.width, "") + "\n")
	}
	b.WriteString("\n")

	if m.mode == "table" {
		if len(m.report.Findings) == 0 {
			b.WriteString(m.styles.Muted.Render("no findings in this report\n"))
		} else {
			b.WriteString(m.table.View() + "\n")
			b.WriteString(m.styles.Muted.Render("↑/↓ select · enter detail · q quit\n"))
		}
		return tea.NewView(b.String())
	}
	frame := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(theme.Palette.Muted).Padding(0, 1).Render(m.viewport.View())
	b.WriteString(frame + "\n")
	b.WriteString(m.styles.Muted.Render("↑/↓ scroll · esc back · q quit\n"))
	v := tea.NewView(b.String())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// BrowseReport runs the report browser TUI.
func BrowseReport(rep *views.Report) error {
	p := tea.NewProgram(newReportModel(rep, 100, 40))
	_, err := p.Run()
	return err
}

func shortPath(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) > 3 {
		return "…/" + strings.Join(parts[len(parts)-3:], "/")
	}
	return p
}
