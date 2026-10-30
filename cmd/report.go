package cmd

import (
	"charm.land/lipgloss/v2"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/traust-security/traust-cli/internal/api"
	"github.com/traust-security/traust-cli/internal/theme"
	"github.com/traust-security/traust-cli/internal/tui"
	"github.com/traust-security/traust-cli/internal/views"
)

var reportFlags struct {
	validate bool
	render   bool
}

var reportCmd = &cobra.Command{
	Use:   "report <file-or-dir>",
	Short: "Validate and browse a report",
	Long: `Validates report JSON against its schema (traust reporting validate),
lints threat models (traust reporting lint), renders the Markdown companion
(traust reporting render), and on a TTY opens a severity-colored findings
browser. Read-only.`,
	Args: cobra.ExactArgs(1),
	Run:  runReport,
}

func init() {
	rootCmd.AddCommand(reportCmd)
	reportCmd.Flags().BoolVar(&reportFlags.validate, "validate", true, "run schema validation")
	reportCmd.Flags().BoolVar(&reportFlags.render, "render", false, "regenerate the Markdown companion")
}

func runReport(cmd *cobra.Command, args []string) {
	path := args[0]
	state, err := loadState()
	if err != nil {
		fatalf(1, "%v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		fatalf(1, "%v", err)
	}
	var files []string
	if info.IsDir() {
		files, err = views.DiscoverReports(path)
		if err != nil {
			fatalf(1, "%v", err)
		}
		if len(files) == 0 {
			fatalf(1, "no .json reports in %s", path)
		}
	} else {
		files = []string{path}
	}

	client := api.NewPythonCLI(state.VenvPython(), state.ChildEnv(), state.TraustRepo())
	ctx := context.Background()
	styles := theme.Default()

	for _, f := range files {
		if reportFlags.validate {
			if strings.HasSuffix(f, "-threat-model.md") || filepath.Base(f) == "THREAT_MODEL.md" {
				res, err := client.ReportingLint(ctx, []string{f}, false)
				if err != nil {
					fatalf(1, "lint: %v", err)
				}
				mark := styles.OK.Render("✓")
				if res.ExitCode != 0 {
					mark = styles.Err.Render("✗")
				}
				lipgloss.Fprintf(os.Stdout, "  %s lint %s\n", mark, f)
				if res.ExitCode != 0 {
					lipgloss.Fprintln(os.Stdout, indent(strings.TrimSpace(res.Output()), "      "))
				}
				continue
			}
			res, err := client.ReportingValidate(ctx, f, false)
			if err != nil {
				fatalf(1, "validate: %v", err)
			}
			mark := styles.OK.Render("✓")
			if res.ExitCode != 0 {
				mark = styles.Err.Render("✗")
			}
			lipgloss.Fprintf(os.Stdout, "  %s validate %s\n", mark, f)
			if res.ExitCode != 0 {
				lipgloss.Fprintln(os.Stdout, indent(strings.TrimSpace(res.Output()), "      "))
				continue
			}
		}
		if reportFlags.render && strings.HasSuffix(f, ".json") {
			out := strings.TrimSuffix(f, ".json") + ".md"
			res, err := client.ReportingRender(ctx, f, out)
			if err != nil {
				fatalf(1, "render: %v", err)
			}
			if res.ExitCode == 0 {
				lipgloss.Fprintf(os.Stdout, "  %s render %s\n", styles.OK.Render("✓"), out)
			} else {
				lipgloss.Fprintf(os.Stdout, "  %s render %s\n", styles.Err.Render("✗"), f)
				lipgloss.Fprintln(os.Stdout, indent(strings.TrimSpace(res.Output()), "      "))
			}
		}
	}

	if len(files) == 1 && strings.HasSuffix(files[0], ".json") && isTTY() {
		rep, err := views.LoadReport(files[0])
		if err == nil && len(rep.Findings) > 0 {
			if err := tui.BrowseReport(rep); err != nil {
				fatalf(1, "browser: %v", err)
			}
		}
	}
}
