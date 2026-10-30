package cmd

import (
	"charm.land/lipgloss/v2"
	"encoding/json"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/traust-security/traust-cli/internal/catalog"
	"github.com/traust-security/traust-cli/internal/theme"
	"github.com/traust-security/traust-cli/internal/tui"
)

var skillsFlags struct {
	json  bool
	stage string
	tier  string
	all   bool
}

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Browse the skill catalog",
	Long: `Every skill in the harness, parsed from the same SKILL.md frontmatter
the harness itself reads. Interactive terminals get a picker that chains
straight into a run; piped output gets a table.`,
	Run: runSkills,
}

func init() {
	rootCmd.AddCommand(skillsCmd)
	skillsCmd.Flags().BoolVar(&skillsFlags.json, "json", false, "machine-readable listing")
	skillsCmd.Flags().StringVar(&skillsFlags.stage, "stage", "", "filter by stage directory (e.g. 3-audit)")
	skillsCmd.Flags().StringVar(&skillsFlags.tier, "tier", "", "filter by tier (primary, secondary, tertiary, ci)")
	skillsCmd.Flags().BoolVar(&skillsFlags.all, "all", false, "include campaign-only skills (default: standalone profile)")
}

func loadCatalog() (*catalog.Catalog, error) {
	state, err := loadState()
	if err != nil {
		return nil, err
	}
	return catalog.Load(state.TraustRepo())
}

func runSkills(cmd *cobra.Command, args []string) {
	cat, err := loadCatalog()
	if err != nil {
		fatalf(1, "%v", err)
	}

	var skills = cat.Skills()
	if !skillsFlags.all {
		skills = cat.StandaloneSkills()
	}
	if skillsFlags.stage != "" {
		var filtered []*catalog.Skill
		for _, s := range skills {
			if s.Stage == skillsFlags.stage {
				filtered = append(filtered, s)
			}
		}
		skills = filtered
	}
	if skillsFlags.tier != "" {
		var filtered []*catalog.Skill
		for _, s := range skills {
			if s.Tier == skillsFlags.tier {
				filtered = append(filtered, s)
			}
		}
		skills = filtered
	}

	if skillsFlags.json {
		out, _ := json.MarshalIndent(skills, "", "  ")
		lipgloss.Fprintln(os.Stdout, string(out))
		return
	}

	if isTTY() && len(skills) > 0 {
		skill, err := tui.PickSkill(tui.SortForPicker(skills))
		if err != nil {
			fatalf(1, "picker: %v", err)
		}
		if skill == nil {
			return
		}
		runSkillInteractive(skill)
		return
	}

	styles := theme.Default()
	sort.Slice(skills, func(i, j int) bool {
		if skills[i].StageNum != skills[j].StageNum {
			return skills[i].StageNum < skills[j].StageNum
		}
		return skills[i].Name < skills[j].Name
	})
	// Column layout adapts to the terminal. Below ~77 columns there is no
	// room for a description column, so the table drops it rather than
	// pushing rows past the right edge.
	width := tui.TerminalWidth()
	avail := width - 2
	if avail < 28+15+11+3+20 {
		lipgloss.Fprintf(os.Stdout, "  %-28s %-15s %-11s\n", styles.Label.Render("SKILL"), styles.Label.Render("STAGE"), styles.Label.Render("MIN MODEL"))
		for _, s := range skills {
			lipgloss.Fprintf(os.Stdout, "  %-28s %-15s %-11s\n",
				tui.Truncate(s.Name, 28, "…"), tui.Truncate(orDash(s.Stage), 15, "…"), minModelOf(s))
		}
	} else {
		descWidth := avail - 28 - 15 - 11 - 3
		lipgloss.Fprintf(os.Stdout, "  %-28s %-15s %-11s %s\n", styles.Label.Render("SKILL"), styles.Label.Render("STAGE"), styles.Label.Render("MIN MODEL"), styles.Label.Render("DESCRIPTION"))
		for _, s := range skills {
			lipgloss.Fprintf(os.Stdout, "  %-28s %-15s %-11s %s\n",
				tui.Truncate(s.Name, 28, "…"), tui.Truncate(orDash(s.Stage), 15, "…"), minModelOf(s), tui.Truncate(s.Description, descWidth, "…"))
		}
	}
	lipgloss.Fprintln(os.Stdout)
	lipgloss.Fprintln(os.Stdout, styles.Muted.Render("run one: traust-cli run <skill> [args…] --harness <id>"))
	os.Exit(0)
}

func minModelOf(s *catalog.Skill) string {
	if s.Standalone != nil && s.Standalone.MinModelClass != "" {
		return s.Standalone.MinModelClass
	}
	return "—"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
