package tui

import (
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/traust-security/traust-cli/internal/catalog"
	"github.com/traust-security/traust-cli/internal/theme"
)

// SkillPick is the outcome of the skill picker + run form.
type SkillPick struct {
	Skill   *catalog.Skill
	Args    string
	Harness string
	Model   string
}

type skillItem struct {
	skill *catalog.Skill
}

func (i skillItem) Title() string { return i.skill.Name }

func (i skillItem) Description() string {
	parts := []string{}
	if i.skill.Stage != "" {
		parts = append(parts, i.skill.Stage)
	}
	if i.skill.Tier != "" {
		parts = append(parts, i.skill.Tier)
	}
	if i.skill.Standalone != nil && i.skill.Standalone.MinModelClass != "" {
		parts = append(parts, i.skill.Standalone.MinModelClass+"-class")
	}
	if i.skill.Standalone == nil {
		parts = append(parts, "campaign-only")
	}
	return strings.Join(parts, " · ") + " — " + i.skill.Description
}

func (i skillItem) FilterValue() string { return i.skill.Name + " " + i.skill.Description }

type pickerModel struct {
	list     list.Model
	skills   []*catalog.Skill
	styles   theme.Styles
	quitting bool
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetSize(msg.Width-4, msg.Height-6)
		return m, nil
	case tea.KeyPressMsg:
		if key.Matches(msg, m.list.KeyMap.ForceQuit) {
			m.quitting = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m pickerModel) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	title := m.styles.App.Render(" traust ") + m.styles.Header.Render(" skills")
	v := tea.NewView("\n" + title + "\n" + m.list.View())
	v.AltScreen = true
	return v
}

// PickSkill runs the skill picker TUI. It returns the selected skill, or nil
// when the user quits without selecting.
func PickSkill(skills []*catalog.Skill) (*catalog.Skill, error) {
	items := make([]list.Item, 0, len(skills))
	for _, s := range skills {
		items = append(items, skillItem{skill: s})
	}
	styles := theme.Default()
	delegate := list.NewDefaultDelegate()
	l := list.New(items, delegate, 80, 20)
	l.Title = "select a skill (enter to run, / to filter)"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.Styles.Title = styles.Header
	l.AdditionalShortHelpKeys = nil
	l.AdditionalFullHelpKeys = nil

	p := tea.NewProgram(pickerModel{list: l, skills: skills, styles: styles})
	out, err := p.Run()
	if err != nil {
		return nil, err
	}
	m, ok := out.(pickerModel)
	if !ok || m.quitting {
		return nil, nil
	}
	sel, ok := m.list.SelectedItem().(skillItem)
	if !ok {
		return nil, nil
	}
	return sel.skill, nil
}

// SortForPicker orders skills: standalone first (by stage), then the rest.
func SortForPicker(skills []*catalog.Skill) []*catalog.Skill {
	out := make([]*catalog.Skill, len(skills))
	copy(out, skills)
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := out[i].Standalone != nil, out[j].Standalone != nil
		if si != sj {
			return si
		}
		if out[i].StageNum != out[j].StageNum {
			return out[i].StageNum < out[j].StageNum
		}
		return out[i].Name < out[j].Name
	})
	return out
}
