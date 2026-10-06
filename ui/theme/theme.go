// Package theme owns the active, swappable palette.
package theme

import (
	"image/color"
	"maps"
	"slices"

	"charm.land/lipgloss/v2"
)

// DefaultName is the theme active until Apply, and used when unset.
// Duplicated as config.DefaultTheme rather than imported.
const DefaultName = "dark"

// Themes is every built-in scheme, keyed by name.
var Themes = map[string]Theme{
	"dark":      dark,
	"light":     light,
	"solarized": solarized,
	"dracula":   dracula,
}

// active is the palette Apply last set, the default until it is called.
var active = Themes[DefaultName]

// Theme is one named color scheme.
type Theme struct {
	Accent                                    color.Color
	Safe, Caution, Danger                     color.Color
	TextPrimary, TextMuted, TextFaint, Border color.Color

	// Background is painted first, so the palette need not match the terminal.
	Background color.Color

	// Markdown names the closest glamour style, since glamour cannot take ours.
	Markdown string

	// Syntax names the chroma style a diff's code is coloured in, and DiffAdded
	// and DiffRemoved tint the lines a change added and removed beneath it.
	Syntax                 string
	DiffAdded, DiffRemoved color.Color

	// Tools colour a tool's name in history by what it does, so each is a hue
	// far from the others rather than a shade of the text.
	Tools ToolColors
}

// ToolColors are the shell, reading, writing, the web, a server's tool, a
// subagent, a reviewer, and anything else such as a skill.
type ToolColors struct {
	Shell, Read, Write, Web, Server, Agent, Review, Other color.Color
}

// Names lists every valid theme name, sorted.
func Names() []string {
	return slices.Sorted(maps.Keys(Themes))
}

// Current is the active theme.
func Current() Theme { return active }

// Apply makes t the active theme. Restyle afterwards, as ui.RefreshStyles does.
// Not safe for concurrent use: call it once, before the TUI starts.
func Apply(t Theme) { active = t }

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
	Syntax:      "github-dark",
	DiffAdded:   lipgloss.Color("#14261C"),
	DiffRemoved: lipgloss.Color("#2C1517"),
	Tools: ToolColors{
		Shell: lipgloss.Color("#C792EA"), Read: lipgloss.Color("#4FA8FF"), Write: lipgloss.Color("#F2C14E"),
		Web: lipgloss.Color("#3DDC84"), Server: lipgloss.Color("#FF5C5C"),
		Agent: lipgloss.Color("#F037D0"), Review: lipgloss.Color("#00E5FF"), Other: lipgloss.Color("#7A8296"),
	},
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
	Syntax:      "github",
	DiffAdded:   lipgloss.Color("#E6FFEC"),
	DiffRemoved: lipgloss.Color("#FFEBE9"),
	Tools: ToolColors{
		Shell: lipgloss.Color("#8250DF"), Read: lipgloss.Color("#0B6BD3"), Write: lipgloss.Color("#A87000"),
		Web: lipgloss.Color("#15803D"), Server: lipgloss.Color("#CF222E"),
		Agent: lipgloss.Color("#F037D0"), Review: lipgloss.Color("#1A1A9E"), Other: lipgloss.Color("#6B7280"),
	},
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
	Syntax:      "solarized-dark256",
	DiffAdded:   lipgloss.Color("#0B3A2C"),
	DiffRemoved: lipgloss.Color("#3A1B20"),
	Tools: ToolColors{
		Shell: lipgloss.Color("#C792EA"), Read: lipgloss.Color("#4FA8FF"), Write: lipgloss.Color("#F2C14E"),
		Web: lipgloss.Color("#3DDC84"), Server: lipgloss.Color("#FF5C5C"),
		Agent: lipgloss.Color("#F037D0"), Review: lipgloss.Color("#00E5FF"), Other: lipgloss.Color("#7A8296"),
	},
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
	Syntax:      "dracula",
	DiffAdded:   lipgloss.Color("#1F3A2B"),
	DiffRemoved: lipgloss.Color("#3A1F2A"),
	Tools: ToolColors{
		Shell: lipgloss.Color("#BD93F9"), Read: lipgloss.Color("#8BE9FD"), Write: lipgloss.Color("#F1FA8C"),
		Web: lipgloss.Color("#50FA7B"), Server: lipgloss.Color("#FF5555"),
		Agent: lipgloss.Color("#F037D0"), Review: lipgloss.Color("#00E5FF"), Other: lipgloss.Color("#6272A4"),
	},
}
