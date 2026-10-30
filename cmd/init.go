package cmd

import (
	"charm.land/lipgloss/v2"
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/traust-security/traust-cli/internal/bootstrap"
	"github.com/traust-security/traust-cli/internal/harness"
	"github.com/traust-security/traust-cli/internal/theme"
	"github.com/traust-security/traust-cli/internal/tui"
)

var initOpts struct {
	workspace      string
	forge          string
	ref            string
	configHome     string
	toolchain      string
	harness        string
	fullPermission bool
	writeProfile   bool
	yes            bool
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Set up the managed traust workspace",
	Long: `Bootstrap everything the workflow needs, idempotently:

  1. uv (pinned release, checksum-verified) and Python 3.12
  2. the traust checkout, pinned to a stable tag
  3. uv sync — engine, ledger and contracts from their git pins
  4. TRAUST_CONFIG_HOME from the shipped templates (via install_traust)
  5. an optional scanner toolchain profile

Nothing is written to your shell profile by default: the CLI injects
TRAUST_CONFIG_HOME and the venv PATH into the processes it spawns. Pass
--write-profile to also record the variable the classic way.`,
	Run: runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().StringVar(&initOpts.workspace, "workspace", "", "managed workspace root (default ~/.traust)")
	initCmd.Flags().StringVar(&initOpts.forge, "forge", "", "git URL to clone traust from (default: the public traust repository)")
	initCmd.Flags().StringVar(&initOpts.ref, "ref", "latest", "tag or branch to pin (or \"latest\" for the newest stable tag)")
	initCmd.Flags().StringVar(&initOpts.configHome, "config-home", "", "TRAUST_CONFIG_HOME (default <workspace>/config)")
	initCmd.Flags().StringVar(&initOpts.toolchain, "toolchain", "secure-code-audit", "scanner toolchain profile to install (secure-code-audit covers every flow stage; 'none' skips)")
	initCmd.Flags().StringVar(&initOpts.harness, "harness", "", "default agent harness (default: first detected)")
	initCmd.Flags().BoolVar(&initOpts.fullPermission, "full-permission", false, "consent to full-permission runs: harness executes with all tools auto-approved (required by the harness's skills)")
	initCmd.Flags().BoolVar(&initOpts.writeProfile, "write-profile", false, "also record TRAUST_CONFIG_HOME in your shell profile (install_traust --write-profile)")
	initCmd.Flags().BoolVarP(&initOpts.yes, "yes", "y", false, "non-interactive: accept all defaults")
}

// defaultForge is the wizard's placeholder for the forge URL.
func defaultForge() string {
	if initOpts.forge != "" {
		return initOpts.forge
	}
	return bootstrap.DefaultForge
}

func runInit(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	styles := theme.Default()

	workspace := initOpts.workspace
	if workspace == "" {
		w, err := bootstrap.DefaultWorkspace()
		if err != nil {
			fatalf(1, "%v", err)
		}
		workspace = w
	}

	interactive := isTTY() && !initOpts.yes
	if interactive {
		detected := detectHarnessIDs(ctx)
		defaultHarness := initOpts.harness
		if defaultHarness == "" && len(detected) > 0 {
			defaultHarness = detected[0]
		}
		if defaultHarness == "" {
			defaultHarness = "claude"
		}
		answers, err := tui.RunInitWizard(workspace, defaultForge(), detected, defaultHarness)
		if err != nil {
			fatalf(1, "init wizard: %v", err)
		}
		workspace = answers.Workspace
		initOpts.forge = answers.Forge
		initOpts.ref = answers.Ref
		initOpts.harness = answers.Harness
		initOpts.fullPermission = answers.FullPermission
	}

	log := func(format string, a ...any) {
		lipgloss.Fprintln(os.Stdout, tui.Wrap(styles.Accent.Render(fmt.Sprintf(format, a...)), tui.TerminalWidth()))
	}

	state, err := bootstrap.Init(ctx, bootstrap.InitOptions{
		Workspace:        workspace,
		Forge:            initOpts.forge,
		Ref:              initOpts.ref,
		ConfigHome:       initOpts.configHome,
		ToolchainProfile: initOpts.toolchain,
		WriteProfile:     initOpts.writeProfile,
		Harness:          initOpts.harness,
		FullPermission:   initOpts.fullPermission,
	}, log)
	if err != nil {
		fatalf(1, "init: %v", err)
	}

	section("Workspace ready")
	kv("workspace", state.Workspace)
	kv("traust", fmt.Sprintf("%s @ %s", state.TraustRef, shortSHA(state.TraustSHA)))
	kv("config home", state.ConfigHome)
	kv("default harness", state.Harness)

	lipgloss.Fprintln(os.Stdout)
	lipgloss.Fprintln(os.Stdout, styles.OK.Render("Next steps:"))
	lipgloss.Fprintln(os.Stdout, "  traust-cli doctor                       verify the install end to end")
	lipgloss.Fprintln(os.Stdout, "  traust-cli skills                       browse the catalog and run a skill")
	lipgloss.Fprintln(os.Stdout, "  traust-cli run threat-model <repo>      first audit: threat model a repository")
}

func detectHarnessIDs(ctx context.Context) []string {
	var ids []string
	for id, d := range harness.DetectAll(ctx) {
		if d.Available {
			ids = append(ids, id)
		}
	}
	// Preserve priority order.
	var ordered []string
	for _, id := range harness.IDs() {
		for _, found := range ids {
			if found == id {
				ordered = append(ordered, id)
				break
			}
		}
	}
	return ordered
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// isTTY reports whether the CLI can render interactive screens: both stdin
// and stdout must be real terminals. The check is ioctl-based — /dev/null is
// a character device but not a terminal, and treating it as one hangs the
// picker waiting for input that will never arrive.
func isTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}
