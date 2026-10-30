package cmd

import (
	"charm.land/lipgloss/v2"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/traust-security/traust-cli/internal/theme"
	"github.com/traust-security/traust-cli/internal/tui"
)

// version is set at build time via -ldflags.
var version = "0.1.0"

var rootCmd = &cobra.Command{
	Use:   "traust-cli",
	Short: "One binary for the traust security workflow",
	Long: `traust-cli wraps the traust harness end to end:

  init      set up the managed workspace (uv, traust checkout, config home)
  doctor    verify the install
  skills    browse the skill catalog
  run       execute a skill headlessly through your agent harness
  report    validate and browse a report
  ledger    read-only ledger views

The CLI delegates every deterministic operation to the traust engine CLI and
adds the user experience: catalog, harness adapters (claude, codex, pi,
opencode, crush, goose), model-floor pre-flight, budget bands and run
journals.`,
	Version:       version,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() {
	installWrappedUsageTemplate()
	if err := rootCmd.Execute(); err != nil {
		styles := theme.Default()
		lipgloss.Fprintln(os.Stderr, styles.Err.Render("error: "+err.Error()))
		os.Exit(1)
	}
}

// installWrappedUsageTemplate makes cobra's help respect the terminal width.
// The default template emits flag usages unwrapped, so long descriptions
// hard-wrap at the terminal's right edge with continuations at column zero
// while the flags themselves are indented. pflag already knows how to wrap
// (FlagUsagesWrapped aligns continuations under each description); the
// template is patched in place rather than copied, so it tracks cobra's
// default. Long descriptions and the trailing use-hint are wrapped the same
// way, and cobra's built-in completion commands are hidden (their summaries
// are too long for narrow terminals and they remain invocable). Templates
// inherit from the root, so every subcommand is covered.
func installWrappedUsageTemplate() {
	width := tui.TerminalWidth()
	if width < 60 {
		width = 60
	}
	tmpl := rootCmd.UsageTemplate()
	patched := strings.ReplaceAll(tmpl,
		".FlagUsages | trimTrailingWhitespaces",
		fmt.Sprintf(".FlagUsagesWrapped %d | trimTrailingWhitespaces", width))
	if width < 78 {
		patched = strings.ReplaceAll(patched,
			"for more information about a command.",
			"for more\ninformation about a command.")
	}
	if patched != tmpl {
		rootCmd.SetUsageTemplate(patched)
	}
	rootCmd.CompletionOptions.HiddenDefaultCmd = true
	wrapCommandLongs(rootCmd, width)
}

// wrapCommandLongs re-wraps every command's Long description to the current
// width; cobra renders them verbatim.
func wrapCommandLongs(cmd *cobra.Command, width int) {
	if cmd.Long != "" {
		cmd.Long = tui.Wrap(cmd.Long, width)
	}
	for _, sub := range cmd.Commands() {
		wrapCommandLongs(sub, width)
	}
}

func init() {
	rootCmd.SetVersionTemplate(theme.Default().App.Render(" traust-cli ") + " {{.Version}}\n")
}

// fatalf prints a styled error and exits with the given code.
func fatalf(code int, format string, args ...any) {
	lipgloss.Fprintln(os.Stderr, theme.Default().Err.Render("error: "+fmt.Sprintf(format, args...)))
	os.Exit(code)
}

// section prints a section header.
func section(title string) {
	lipgloss.Fprintln(os.Stdout)
	lipgloss.Fprintln(os.Stdout, theme.Default().Header.Render(title))
}

// kv prints a label/value line, wrapping the value to the terminal width.
func kv(label, value string) {
	styles := theme.Default()
	const lead = 18 // "  " + 14-wide label + " "
	width := tui.TerminalWidth() - lead
	if width < 20 {
		width = 20
	}
	lipgloss.Fprintf(os.Stdout, "  %s %s\n", styles.Label.Render(pad(label)), tui.Wrap(value, width))
}

func pad(s string) string {
	if len(s) >= 14 {
		return s
	}
	return s + strings.Repeat(" ", 14-len(s))
}
