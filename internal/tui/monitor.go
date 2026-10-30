// Package tui contains the interactive screens: the run monitor, the skill
// picker, the report browser and the init wizard.
package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/traust-security/traust-cli/internal/harness"
	"github.com/traust-security/traust-cli/internal/theme"
)

// MonitorHeader describes the run for the monitor's header bar.
type MonitorHeader struct {
	Skill     string
	Harness   string
	Model     string
	BudgetCap int64
	// RunID is shown short in the header (attach context).
	RunID string
	// StartedAt, when non-zero, anchors the elapsed clock (attach replays
	// runs that may have started long before the monitor opened).
	StartedAt time.Time
}

// ItemKind classifies a monitor feed item.
type ItemKind int

const (
	// ItemEvent is one normalized harness event.
	ItemEvent ItemKind = iota
	// ItemRaw is one unparsed harness output line.
	ItemRaw
	// ItemProgress is a run-stage message.
	ItemProgress
	// ItemDone marks the run as complete.
	ItemDone
)

// MonitorItem is one unit of monitor input. Live runs and journal replays
// both produce them.
type MonitorItem struct {
	Kind  ItemKind
	Event harness.Event
	Text  string
}

type tickMsg time.Time

type monitorModel struct {
	header              MonitorHeader
	stop                func()
	styles              theme.Styles
	viewport            viewport.Model
	lines               []monitorLine
	tokensIn, tokensOut int64
	cost                float64
	band                string
	done                bool
	failed              bool
	cancelling          bool
	// cancelArmed is the x-key confirmation state: the first press asks,
	// the second (within armWindow) cancels the run. Cancelling a scan is
	// destructive and must never happen on a stray keystroke.
	cancelArmed   bool
	cancelArmedAt time.Time
	lastOutput    time.Time
	started       time.Time
	width, height int
}

// monitorLine is one transcript line; keyed lines are updated in place by
// streaming parts (crush rewrites message content as it grows), unkeyed
// lines append.
type monitorLine struct {
	key  string
	text string
}

const armWindow = 6 * time.Second

func newMonitor(header MonitorHeader, stop func(), width, height int) monitorModel {
	vp := viewport.New(viewport.WithWidth(width-2), viewport.WithHeight(height-8))
	started := header.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	return monitorModel{
		header: header, stop: stop, styles: theme.Default(),
		viewport: vp, band: "within", started: started,
		width: width, height: height,
	}
}

func (m monitorModel) Init() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m monitorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		// Disarm the cancel confirmation when the window passes.
		if m.cancelArmed && time.Since(m.cancelArmedAt) > armWindow {
			m.cancelArmed = false
		}
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
	case MonitorItem:
		m.lastOutput = time.Now()
		switch msg.Kind {
		case ItemEvent:
			m.applyEvent(msg.Event)
		case ItemRaw:
			m.pushLine(m.styles.Muted.Render("  " + msg.Text))
		case ItemProgress:
			style := m.styles.Accent
			if strings.HasPrefix(msg.Text, "warning:") || strings.HasPrefix(msg.Text, "note:") {
				style = m.styles.Warn
			}
			if strings.Contains(msg.Text, "failed") || strings.Contains(msg.Text, "refused") {
				style = m.styles.Err
				m.failed = true
			}
			if strings.Contains(msg.Text, "is not runnable") {
				style = m.styles.Err
				m.failed = true
			}
			if rest, ok := strings.CutPrefix(msg.Text, "budget band "); ok {
				if fields := strings.Fields(rest); len(fields) > 0 {
					m.band = fields[0]
				}
			}
			m.pushLine(style.Render("› " + msg.Text))
		case ItemDone:
			m.done = true
			m.pushLine(m.styles.Muted.Render("— run complete —"))
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.viewport.SetWidth(msg.Width - 2)
		m.viewport.SetHeight(msg.Height - 8)
		m.rebuild()
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q", "d", "esc":
			// Detach: the run keeps going in its own process. This is the
			// common case and must be instant and non-destructive.
			return m, tea.Quit
		case "enter":
			if m.done {
				return m, tea.Quit
			}
		case "x":
			if m.done {
				return m, tea.Quit
			}
			if m.cancelArmed {
				m.cancelling = true
				m.pushLine(m.styles.Warn.Render("› cancelling — stopping the run…"))
				if m.stop != nil {
					m.stop()
				}
				return m, nil
			}
			m.cancelArmed = true
			m.cancelArmedAt = time.Now()
			m.pushLine(m.styles.Warn.Render("› press x again to cancel the run (ctrl+c detaches without cancelling)"))
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *monitorModel) applyEvent(ev harness.Event) {
	switch ev.Kind {
	case harness.EventText:
		if ev.Key != "" {
			m.upsertLine(ev.Key, ev.Text)
		} else {
			m.pushLine(ev.Text)
		}
	case harness.EventThinking:
		m.upsertLine(ev.Key, m.styles.Muted.Italic(true).Render("· "+ev.Text))
	case harness.EventToolCall:
		line := "→ " + ev.Tool
		if ev.Text != "" {
			line += "  " + ev.Text
		}
		m.upsertLine(ev.Key, m.styles.Accent.Render(line))
	case harness.EventToolResult:
		// The tool's actual output, already truncated by the adapter.
		if ev.Text == "" {
			return
		}
		style := m.styles.Muted
		prefix := "  │ "
		if ev.IsError {
			style = m.styles.Warn
		}
		var b strings.Builder
		for _, l := range strings.Split(ev.Text, "\n") {
			b.WriteString(style.Render(prefix+l) + "\n")
		}
		m.upsertLine(ev.Key, strings.TrimRight(b.String(), "\n"))
	case harness.EventUsage:
		m.tokensIn, m.tokensOut = ev.TokensIn, ev.TokensOut
	case harness.EventResult:
		m.tokensIn, m.tokensOut = ev.TokensIn, ev.TokensOut
		m.cost = ev.CostUSD
		if ev.Text != "" {
			m.pushLine(m.styles.OK.Render("✓ " + ev.Text))
		}
	case harness.EventError:
		m.failed = true
		m.pushLine(m.styles.Err.Render("✗ " + ev.Text))
	case harness.EventNeedsInput:
		m.failed = true
		what := ev.Tool
		if what == "" {
			what = ev.Text
		}
		m.pushLine(m.styles.Err.Render("✗ needs input that a headless run cannot provide: " + what))
	}
}

// pushLine appends one or more lines (a multi-line string splits).
func (m *monitorModel) pushLine(line string) {
	m.upsertLine("", line)
}

// upsertLine writes a line: keyed lines update the line registered under
// the key in place (creating it at the end when new), unkeyed lines
// append. Streaming parts rewrite themselves instead of piling up
// fragments.
func (m *monitorModel) upsertLine(key, line string) {
	line = strings.ReplaceAll(line, "\r", "")
	if key == "" {
		for _, l := range strings.Split(line, "\n") {
			m.lines = append(m.lines, monitorLine{text: l})
		}
	} else {
		at := -1
		for i := range m.lines {
			if m.lines[i].key == key {
				at = i
				break
			}
		}
		if at < 0 {
			m.lines = append(m.lines, monitorLine{key: key, text: line})
		} else {
			m.lines[at].text = line
		}
	}
	if len(m.lines) > 1000 {
		m.lines = m.lines[len(m.lines)-1000:]
	}
	m.rebuild()
}

// rebuild re-wraps every retained line at the current viewport width. This
// is what makes a terminal resize actually take effect: previously pushed
// lines re-flow instead of staying clipped to whatever the width was when
// they arrived.
func (m *monitorModel) rebuild() {
	width := m.viewport.Width()
	if width < 4 {
		width = 4
	}
	wrapped := make([]string, 0, len(m.lines))
	for _, l := range m.lines {
		wrapped = append(wrapped, Wrap(l.text, width))
	}
	m.viewport.SetContent(strings.Join(wrapped, "\n"))
	m.viewport.GotoBottom()
}

func (m monitorModel) View() tea.View {
	var b strings.Builder

	elapsed := time.Since(m.started).Round(time.Second)
	status := m.styles.OK.Render("running")
	if m.done {
		if m.failed {
			status = m.styles.Err.Render("finished (with errors)")
		} else {
			status = m.styles.OK.Render("finished")
		}
	} else if m.cancelling {
		status = m.styles.Warn.Render("cancelling…")
	}
	title := m.styles.App.Render(" traust ")
	shortID := ""
	if m.header.RunID != "" {
		parts := strings.Split(m.header.RunID, "-")
		last := parts[len(parts)-1]
		if len(parts) >= 4 && len(last) == 4 {
			shortID = " " + last + " "
		}
	}
	head := m.styles.Header.Render(fmt.Sprintf("%s %s · %s · %s · %s · %s", shortID, m.header.Skill, m.header.Harness, ellipsize(m.header.Model, 40), elapsed, status))
	// The header must fit one line at any width: clip after assembly rather
	// than letting the terminal hard-wrap it into the transcript frame.
	head = Truncate(head, maxInt(m.width-lipgloss.Width(title), 10), "…")
	b.WriteString(title + head)
	b.WriteString("\n\n")

	body := m.viewport.View()
	frame := lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, true, false, true).
		BorderForeground(theme.Palette.Muted).Render(body)
	b.WriteString(frame)
	b.WriteString("\n")

	// Status bar: usage + budget bar + output age. The age indicator is the
	// answer to "is it stuck?": quiet harnesses look frozen without it.
	usage := fmt.Sprintf("in %s · out %s", human(m.tokensIn), human(m.tokensOut))
	if m.cost > 0 {
		usage += fmt.Sprintf(" · $%.2f", m.cost)
	}
	if !m.done && !m.lastOutput.IsZero() {
		if age := time.Since(m.lastOutput).Round(time.Second); age > 15*time.Second {
			usage += fmt.Sprintf(" · last output %s ago", age)
		}
	}
	var bar string
	if m.header.BudgetCap > 0 {
		// The bar yields its cells to the text on narrow terminals.
		barWidth := clampInt((m.width-lipgloss.Width(usage))/3, 8, 24)
		bar = m.budgetBar(m.tokensOut, m.header.BudgetCap, barWidth)
		usage = usage + "  " + bar
	}
	// The status bar style adds one cell of padding on each side.
	b.WriteString(m.styles.StatusBar.Render(Truncate(usage, maxInt(m.width-2, 20), "")))
	b.WriteString("\n")

	footer := "ctrl+c detach (run continues) · x×2 cancel"
	if m.width < 46 {
		footer = "ctrl+c detach · x cancel"
	}
	if m.cancelArmed && !m.cancelling {
		footer = "press x again to cancel · ctrl+c detaches"
	}
	if m.cancelling {
		footer = "cancelling…"
	}
	if m.done {
		footer = "q quit · journal written"
	}
	b.WriteString(m.styles.Muted.Render(footer))
	b.WriteString("\n")
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m monitorModel) budgetBar(out, cap int64, width int) string {
	if width < 4 {
		width = 4
	}
	ratio := 0.0
	if cap > 0 {
		ratio = float64(out) / float64(cap)
	}
	filled := int(ratio * float64(width))
	if filled > width {
		filled = width
	}
	var style lipgloss.Style
	switch m.band {
	case "warn":
		style = m.styles.BarWarn
	case "degrade":
		style = m.styles.BarDegrade
	case "refuse":
		style = m.styles.BarRefuse
	default:
		style = m.styles.BarOK
	}
	bar := style.Render(strings.Repeat("█", filled)) + m.styles.Muted.Render(strings.Repeat("░", width-filled))
	return fmt.Sprintf("budget %s/%s [%s]", human(out), human(cap), bar)
}

// Attach runs the monitor over a feed of monitor items. It is used both for
// live runs (a producer tailing the worker's journal) and finished runs
// (replay followed by an immediately closed feed). Detaching (ctrl+c, q, d)
// quits the monitor without touching the run; cancelling (x twice) invokes
// stop. The returned bool reports whether the run completed while attached.
func Attach(header MonitorHeader, follow <-chan MonitorItem, stop func()) (bool, error) {
	// bubbletea v2 moved alt-screen and mouse mode onto the View: the
	// monitor's View() sets AltScreen, and cell-motion mouse scrolling is
	// requested the same way (kept off here; the viewport scrolls by keys).
	return attachProgram(header, follow, stop)
}

// attachProgram is Attach with injectable program options (tests feed a
// pipe as input and discard output).
func attachProgram(header MonitorHeader, follow <-chan MonitorItem, stop func(), opts ...tea.ProgramOption) (bool, error) {
	m := newMonitor(header, stop, 80, 24)
	prog := tea.NewProgram(m, opts...)

	// The relay must ABANDON the feed when the program exits, not wait for
	// it to close: detaching means the run — and its journal tailer — keep
	// going. Waiting here held the process (and the terminal) open until a
	// second ctrl+c killed it.
	progDone := make(chan struct{})
	go func() {
		for {
			select {
			case it, ok := <-follow:
				if !ok {
					return
				}
				prog.Send(it)
			case <-progDone:
				return
			}
		}
	}()

	out, err := prog.Run()
	close(progDone)
	if err != nil {
		return false, err
	}
	finished := false
	if fm, ok := out.(monitorModel); ok {
		finished = fm.done
	}
	return finished, nil
}

func ellipsize(s string, n int) string {
	if s == "" {
		return "(default)"
	}
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func human(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
