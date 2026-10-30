package cmd

import (
	"charm.land/lipgloss/v2"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/traust-security/traust-cli/internal/catalog"
	"github.com/traust-security/traust-cli/internal/theme"
)

var catalogCheckRoot string

var catalogCmd = &cobra.Command{
	Use:    "catalog",
	Short:  "Catalog maintenance (drift guard)",
	Hidden: true,
}

var catalogCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Compare the shipped standalone profile against a traust checkout",
	Long: `Fails when the embedded standalone.yaml names skills that no longer
exist in the harness tree (renamed, moved, removed). Run in CI against a
traust checkout to catch drift between the CLI's shipped profile and the
harness.`,
	Run: func(cmd *cobra.Command, args []string) {
		root := catalogCheckRoot
		if root == "" {
			state, err := loadState()
			if err != nil {
				fatalf(1, "no --traust-root given and %v", err)
			}
			root = state.TraustRepo()
		}
		cat, err := catalog.Load(root)
		if err != nil {
			fatalf(1, "%v", err)
		}
		rep := catalog.CheckDrift(cat)
		if len(rep.Missing) == 0 {
			lipgloss.Fprintln(os.Stdout, theme.Default().OK.Render(fmt.Sprintf("✓ standalone profile in sync with %s (%d skills)", root, len(cat.Skills()))))
			return
		}
		styles := theme.Default()
		lipgloss.Fprintln(os.Stdout, styles.Err.Render("✗ standalone profile drift:"))
		for _, name := range rep.Missing {
			lipgloss.Fprintf(os.Stdout, "  - %s: in standalone.yaml but not in the tree\n", name)
		}
		os.Exit(1)
	},
}

func init() {
	rootCmd.AddCommand(catalogCmd)
	catalogCmd.AddCommand(catalogCheckCmd)
	catalogCheckCmd.Flags().StringVar(&catalogCheckRoot, "traust-root", "", "path to a traust checkout (default: the workspace's)")
}
