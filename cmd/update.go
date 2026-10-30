package cmd

import (
	"charm.land/lipgloss/v2"
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/traust-security/traust-cli/internal/bootstrap"
	"github.com/traust-security/traust-cli/internal/theme"
)

var updateVersion string

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update the pinned traust checkout and re-sync",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		state, err := loadState()
		if err != nil {
			fatalf(1, "%v", err)
		}
		log := func(format string, a ...any) {
			lipgloss.Fprintln(os.Stdout, theme.Default().Accent.Render(fmt.Sprintf(format, a...)))
		}
		state, err = bootstrap.Update(ctx, state, updateVersion, log)
		if err != nil {
			fatalf(1, "update: %v", err)
		}
		section("Updated")
		kv("traust", fmt.Sprintf("%s @ %s", state.TraustRef, shortSHA(state.TraustSHA)))
		kv("config home", state.ConfigHome+" (files preserved)")
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
	updateCmd.Flags().StringVar(&updateVersion, "version", "latest", "tag or branch to move to (default: newest stable tag)")
}
