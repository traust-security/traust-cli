// Package theme defines the traust-cli visual language: a dark-first palette
// built from the Red Hat design system color tokens (ux.redhat.com) rendered
// through lipgloss.
package theme

import "image/color"

import "charm.land/lipgloss/v2"

// Red Hat design system color tokens (ux.redhat.com). Hex values are the
// published dark-theme-compatible tokens.
const (
	// RedHatRed is the brand red (ux.redhat.com red-600).
	RedHatRed = "#EE0000"
	// Blue is the link/interaction blue.
	Blue = "#0066CC"
	// Green is success (green-500).
	Green = "#3E8625"
	// Orange is warning (orange-500).
	Orange = "#EC7A08"
	// Yellow is caution (gold).
	Yellow = "#F0AB00"
	// Cyan is informational (cyan-500).
	Cyan = "#007A87"
	// Purple is accent (purple-500).
	Purple = "#6753AC"
	// BackgroundDark is the darkest surface (gray-95).
	BackgroundDark = "#151515"
	// SurfaceDark is a raised surface (gray-90).
	SurfaceDark = "#212427"
	// TextMuted is secondary text (gray-40).
	TextMuted = "#6A6E73"
	// TextOnDark is primary text on dark surfaces.
	TextOnDark = "#FFFFFF"
)

// Palette exposes the raw colors for components that build their own styles.
var Palette = struct {
	Red, Blue, Green, Orange, Yellow, Cyan, Purple color.Color
	Background, Surface, Muted, Text               color.Color
}{
	Red:        lipgloss.Color(RedHatRed),
	Blue:       lipgloss.Color(Blue),
	Green:      lipgloss.Color(Green),
	Orange:     lipgloss.Color(Orange),
	Yellow:     lipgloss.Color(Yellow),
	Cyan:       lipgloss.Color(Cyan),
	Purple:     lipgloss.Color(Purple),
	Background: lipgloss.Color(BackgroundDark),
	Surface:    lipgloss.Color(SurfaceDark),
	Muted:      lipgloss.Color(TextMuted),
	Text:       lipgloss.Color(TextOnDark),
}

// Styles is the shared set of lipgloss styles used across the CLI and TUI.
type Styles struct {
	App        lipgloss.Style // program title bar
	Header     lipgloss.Style // section headers
	Label      lipgloss.Style // field labels
	Value      lipgloss.Style // field values
	Muted      lipgloss.Style // secondary text
	OK         lipgloss.Style // success lines
	Warn       lipgloss.Style // warning lines
	Err        lipgloss.Style // error lines
	Accent     lipgloss.Style // highlighted inline text
	BorderBox  lipgloss.Style // bordered panels (detail panes, forms)
	Badge      lipgloss.Style // small inline badges (stage, tier)
	StatusBar  lipgloss.Style // monitor status bar
	BarOK      lipgloss.Style // budget bar fill (within)
	BarWarn    lipgloss.Style // budget bar fill (warn band)
	BarDegrade lipgloss.Style // budget bar fill (degrade band)
	BarRefuse  lipgloss.Style // budget bar fill (refuse band)
}

// Default returns the standard style set.
func Default() Styles {
	return Styles{
		App: lipgloss.NewStyle().
			Foreground(lipgloss.Color(TextOnDark)).
			Background(lipgloss.Color(RedHatRed)).
			Bold(true).
			Padding(0, 1),
		Header: lipgloss.NewStyle().
			Foreground(lipgloss.Color(TextOnDark)).
			Bold(true).
			MarginBottom(1),
		Label: lipgloss.NewStyle().
			Foreground(lipgloss.Color(TextMuted)),
		Value: lipgloss.NewStyle().
			Foreground(lipgloss.Color(TextOnDark)),
		Muted: lipgloss.NewStyle().
			Foreground(lipgloss.Color(TextMuted)),
		OK: lipgloss.NewStyle().
			Foreground(lipgloss.Color(Green)),
		Warn: lipgloss.NewStyle().
			Foreground(lipgloss.Color(Orange)),
		Err: lipgloss.NewStyle().
			Foreground(lipgloss.Color(RedHatRed)),
		Accent: lipgloss.NewStyle().
			Foreground(lipgloss.Color(Blue)),
		BorderBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(TextMuted)).
			Padding(1, 2),
		Badge: lipgloss.NewStyle().
			Foreground(lipgloss.Color(BackgroundDark)).
			Background(lipgloss.Color(Purple)).
			Padding(0, 1),
		StatusBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color(TextOnDark)).
			Background(lipgloss.Color(SurfaceDark)).
			Padding(0, 1),
		BarOK: lipgloss.NewStyle().
			Foreground(lipgloss.Color(Green)),
		BarWarn: lipgloss.NewStyle().
			Foreground(lipgloss.Color(Yellow)),
		BarDegrade: lipgloss.NewStyle().
			Foreground(lipgloss.Color(Orange)),
		BarRefuse: lipgloss.NewStyle().
			Foreground(lipgloss.Color(RedHatRed)),
	}
}

// SeverityStyle maps a report severity string to a colored style. Unknown
// severities render as informational cyan.
func SeverityStyle(styles Styles, severity string) lipgloss.Style {
	switch severity {
	case "critical", "Critical", "CRITICAL":
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(TextOnDark)).Background(lipgloss.Color(RedHatRed)).Padding(0, 1)
	case "high", "High", "HIGH":
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(RedHatRed))
	case "medium", "Medium", "MEDIUM":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(Orange))
	case "low", "Low", "LOW":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(Blue))
	case "none", "info", "Info", "INFO":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(Cyan))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color(TextMuted))
	}
}

// TierStyle maps a harness skill tier to a badge style.
func TierStyle(styles Styles, tier string) lipgloss.Style {
	switch tier {
	case "primary":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(BackgroundDark)).Background(lipgloss.Color(RedHatRed)).Padding(0, 1)
	case "secondary":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(BackgroundDark)).Background(lipgloss.Color(Blue)).Padding(0, 1)
	case "ci":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(BackgroundDark)).Background(lipgloss.Color(Cyan)).Padding(0, 1)
	case "tertiary":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(BackgroundDark)).Background(lipgloss.Color(TextMuted)).Padding(0, 1)
	default:
		return styles.Badge
	}
}
