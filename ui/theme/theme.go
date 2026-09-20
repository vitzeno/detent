// Package theme owns the active, swappable palette.
package theme

import (
	"image/color"
	"sort"

	"charm.land/lipgloss/v2"
)

// Theme is one named color scheme.
type Theme struct {
	Accent                                    color.Color
	Safe, Caution, Danger                     color.Color
	TextPrimary, TextMuted, TextFaint, Border color.Color

	// Background is what the terminal is painted before anything is
	// drawn on it. Without one the palette has to hope the terminal
	// already matches — a light theme's near-black text is invisible
	// on a dark terminal.
	Background color.Color

	// Markdown names the glamour style the prose pane renders with.
	// glamour ships its own palettes and can't be handed ours, so the
	// closest one is named here rather than guessed from the terminal.
	Markdown string
}

// The active palette. Apply reassigns these directly.
var (
	Accent color.Color

	Safe    color.Color
	Caution color.Color
	Danger  color.Color

	TextPrimary color.Color
	TextMuted   color.Color
	TextFaint   color.Color
	Border      color.Color
	Background  color.Color

	// Markdown is the active theme's glamour style name.
	Markdown string
)

// DefaultName is the theme applied at init and used when unset.
// Duplicated as config.DefaultTheme rather than imported.
const DefaultName = "dark"

var dark = Theme{
	Accent:      lipgloss.Color("#45D6C4"),
	Safe:        lipgloss.Color("#6FD98C"),
	Caution:     lipgloss.Color("#E8B24D"),
	Danger:      lipgloss.Color("#F0665E"),
	TextPrimary: lipgloss.Color("#E7ECF3"),
	TextMuted:   lipgloss.Color("#8992A8"),
	TextFaint:   lipgloss.Color("#5C6478"),
	Border:      lipgloss.Color("#333A4D"),
	Background:  lipgloss.Color("#11131A"),
	Markdown:    "dark",
}

var light = Theme{
	Accent:      lipgloss.Color("#1C9C8C"),
	Safe:        lipgloss.Color("#2F9E52"),
	Caution:     lipgloss.Color("#B8791D"),
	Danger:      lipgloss.Color("#C43D34"),
	TextPrimary: lipgloss.Color("#1D2129"),
	TextMuted:   lipgloss.Color("#6B7280"),
	TextFaint:   lipgloss.Color("#9CA3AF"),
	Border:      lipgloss.Color("#D6D9E0"),
	Background:  lipgloss.Color("#FAFAFA"),
	Markdown:    "light",
}

var solarized = Theme{
	Accent:      lipgloss.Color("#268BD2"),
	Safe:        lipgloss.Color("#859900"),
	Caution:     lipgloss.Color("#B58900"),
	Danger:      lipgloss.Color("#DC322F"),
	TextPrimary: lipgloss.Color("#93A1A1"),
	TextMuted:   lipgloss.Color("#839496"),
	TextFaint:   lipgloss.Color("#586E75"),
	Border:      lipgloss.Color("#0B3A45"),
	Background:  lipgloss.Color("#002B36"),
	Markdown:    "dark",
}

var dracula = Theme{
	Accent:      lipgloss.Color("#BD93F9"),
	Safe:        lipgloss.Color("#50FA7B"),
	Caution:     lipgloss.Color("#FFB86C"),
	Danger:      lipgloss.Color("#FF5555"),
	TextPrimary: lipgloss.Color("#F8F8F2"),
	TextMuted:   lipgloss.Color("#A3A3C2"),
	TextFaint:   lipgloss.Color("#6272A4"),
	Border:      lipgloss.Color("#44475A"),
	Background:  lipgloss.Color("#282A36"),
	Markdown:    "dracula",
}

// Themes is every built-in scheme, keyed by name.
var Themes = map[string]Theme{
	"dark":      dark,
	"light":     light,
	"solarized": solarized,
	"dracula":   dracula,
}

// Names lists every valid theme name, sorted.
func Names() []string {
	out := make([]string, 0, len(Themes))
	for name := range Themes {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func init() {
	Apply(dark)
}

// Apply makes t the active theme. Callers must also refresh any style
// already baked from the old colors — see ui.RefreshStyles.
func Apply(t Theme) {
	Accent = t.Accent
	Safe, Caution, Danger = t.Safe, t.Caution, t.Danger
	TextPrimary, TextMuted, TextFaint, Border = t.TextPrimary, t.TextMuted, t.TextFaint, t.Border
	Background = t.Background
	Markdown = t.Markdown
}
