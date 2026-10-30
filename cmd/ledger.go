package cmd

import (
	"charm.land/lipgloss/v2"
	"os"

	"github.com/spf13/cobra"

	"github.com/traust-security/traust-cli/internal/views"
)

var ledgerFlags struct {
	url      string
	tokenEnv string
}

var ledgerCmd = &cobra.Command{
	Use:   "ledger",
	Short: "Read-only ledger views",
	Long: `Query a traust ledger service: identity, findings, layers, integrity
verification. Read-only — no submissions, no countersign.

The service URL comes from --url or TRAUST_LEDGER_URL; the bearer token is
read from the environment variable named by --token-env (default LEDGER_TOKEN).
The CLI stores the variable NAME, never the secret.`,
}

var ledgerWhoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the verified actor for the current token",
	Run: func(cmd *cobra.Command, args []string) {
		v := newLedgerViews()
		out, err := v.Whoami(cmd.Context())
		if err != nil {
			fatalf(1, "whoami: %v", err)
		}
		lipgloss.Fprintln(os.Stdout, out)
	},
}

var ledgerFindingsCmd = &cobra.Command{
	Use:   "findings [layer-id]",
	Short: "List resolved finding dispositions",
	Run: func(cmd *cobra.Command, args []string) {
		v := newLedgerViews()
		layer := ""
		if len(args) > 0 {
			layer = args[0]
		}
		out, err := v.Findings(cmd.Context(), layer, 100)
		if err != nil {
			fatalf(1, "findings: %v", err)
		}
		lipgloss.Fprintln(os.Stdout, out)
	},
}

var ledgerLayersCmd = &cobra.Command{
	Use:   "layers",
	Short: "List layers known to the ledger",
	Run: func(cmd *cobra.Command, args []string) {
		v := newLedgerViews()
		out, err := v.Layers(cmd.Context())
		if err != nil {
			fatalf(1, "layers: %v", err)
		}
		lipgloss.Fprintln(os.Stdout, out)
	},
}

var ledgerVerifyCmd = &cobra.Command{
	Use:   "verify <layer-id>",
	Short: "Check layer integrity (Merkle, signatures)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		v := newLedgerViews()
		out, err := v.Verify(cmd.Context(), args[0], true)
		if err != nil {
			fatalf(1, "verify: %v", err)
		}
		lipgloss.Fprintln(os.Stdout, out)
	},
}

func newLedgerViews() *views.LedgerViews {
	url := ledgerFlags.url
	if url == "" {
		url = os.Getenv("TRAUST_LEDGER_URL")
	}
	if url == "" {
		fatalf(1, "no ledger URL — pass --url or set TRAUST_LEDGER_URL")
	}
	token := os.Getenv(ledgerFlags.tokenEnv)
	return views.NewLedgerViews(url, token)
}

func init() {
	rootCmd.AddCommand(ledgerCmd)
	ledgerCmd.AddCommand(ledgerWhoamiCmd, ledgerFindingsCmd, ledgerLayersCmd, ledgerVerifyCmd)
	ledgerCmd.PersistentFlags().StringVar(&ledgerFlags.url, "url", "", "ledger service base URL (or TRAUST_LEDGER_URL)")
	ledgerCmd.PersistentFlags().StringVar(&ledgerFlags.tokenEnv, "token-env", "LEDGER_TOKEN", "environment variable holding the bearer token")
}
