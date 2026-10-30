package cmd

import (
	"charm.land/lipgloss/v2"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var envJSON bool

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Print the injected child environment",
	Long: `The CLI does not write to your shell profile. It injects the harness
venv PATH, TRAUST_CONFIG_HOME and HARNESS_BIN_DIR into every process it
spawns. This command prints that environment — source it if you want the
same setup in your own shell:

  eval "$(traust-cli env)"`,
	Run: func(cmd *cobra.Command, args []string) {
		state, err := loadState()
		if err != nil {
			fatalf(1, "%v", err)
		}
		env := state.ChildEnv()
		if envJSON {
			out, _ := json.MarshalIndent(env, "", "  ")
			lipgloss.Fprintln(os.Stdout, string(out))
			return
		}
		for _, kv := range env {
			if strings.HasPrefix(kv, "PATH=") || strings.HasPrefix(kv, "TRAUST_CONFIG_HOME=") || strings.HasPrefix(kv, "HARNESS_BIN_DIR=") {
				lipgloss.Fprintf(os.Stdout, "export %s\n", quoteEnv(kv))
			}
		}
	},
}

func quoteEnv(kv string) string {
	key, val, _ := strings.Cut(kv, "=")
	return fmt.Sprintf("%s=%q", key, val)
}

func init() {
	rootCmd.AddCommand(envCmd)
	envCmd.Flags().BoolVar(&envJSON, "json", false, "full environment as JSON")
}
