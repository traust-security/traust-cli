package cmd

import (
	"charm.land/lipgloss/v2"
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/traust-security/traust-cli/internal/bootstrap"
	"github.com/traust-security/traust-cli/internal/theme"
	"github.com/traust-security/traust-cli/internal/tui"
)

var doctorJSON bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Verify the install end to end",
	Long: `Runs the CLI's own checks (state, checkout, venv, engine imports) and
delegates the deployment-config checks to the harness's install_traust
--doctor, which exits non-zero on any missing or unparseable config file.`,
	Run: runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "machine-readable output")
}

func runDoctor(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	state, err := loadState()
	if err != nil {
		fatalf(1, "%v", err)
	}
	report, err := bootstrap.Doctor(ctx, state, nil)
	if err != nil {
		fatalf(1, "doctor: %v", err)
	}
	if doctorJSON {
		out, _ := json.MarshalIndent(report, "", "  ")
		lipgloss.Fprintln(os.Stdout, string(out))
	} else {
		styles := theme.Default()
		for _, c := range report.Checks {
			mark := styles.OK.Render("✓")
			if !c.OK {
				mark = styles.Err.Render("✗")
			}
			lipgloss.Fprintf(os.Stdout, "  %s %s\n", mark, c.Name)
			if c.Detail != "" {
				// Details can be multi-line and long (subprocess output):
				// normalize the block and wrap it to the terminal width.
				lipgloss.Fprintln(os.Stdout, styles.Muted.Render(indent(tui.Wrap(strings.TrimSpace(c.Detail), tui.TerminalWidth()-8), "      ")))
			}
		}
	}
	if !report.OK {
		os.Exit(1)
	}
}
