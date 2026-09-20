package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/render"
	"github.com/vitzeno/detent/ui/status"
	"github.com/vitzeno/detent/ui/theme"
	"github.com/vitzeno/detent/ui/tree"
	"github.com/vitzeno/detent/ui/welcome"
)

var (
	accent color.Color

	safe    color.Color
	caution color.Color
	danger  color.Color

	textPrimary color.Color
	textMuted   color.Color
	textFaint   color.Color
	border      color.Color
)

var (
	styleBrand lipgloss.Style
	styleGoal  lipgloss.Style
	styleMuted lipgloss.Style
	styleFaint lipgloss.Style

	styleSafe    lipgloss.Style
	styleCaution lipgloss.Style
	styleDanger  lipgloss.Style

	styleRowCursor lipgloss.Style
	styleKey       lipgloss.Style
	styleHint      lipgloss.Style

	styleDiffAdd lipgloss.Style
	styleDiffDel lipgloss.Style

	styleIsland       lipgloss.Style
	styleIslandActive lipgloss.Style

	styleConfirmDanger lipgloss.Style
	styleConfirmAccent lipgloss.Style
)

func init() {
	RefreshStyles()
}

// RefreshStyles rebuilds every style baked from a theme color. Call
// after theme.Apply.
func RefreshStyles() {
	accent = theme.Accent
	safe, caution, danger = theme.Safe, theme.Caution, theme.Danger
	textPrimary, textMuted, textFaint, border = theme.TextPrimary, theme.TextMuted, theme.TextFaint, theme.Border

	styleBrand = lipgloss.NewStyle().Foreground(accent).Bold(true)
	styleGoal = lipgloss.NewStyle().Foreground(textPrimary)
	styleMuted = lipgloss.NewStyle().Foreground(textMuted)
	styleFaint = lipgloss.NewStyle().Foreground(textFaint)

	styleSafe = lipgloss.NewStyle().Foreground(safe)
	styleCaution = lipgloss.NewStyle().Foreground(caution).Bold(true)
	styleDanger = lipgloss.NewStyle().Foreground(danger).Bold(true)

	styleRowCursor = lipgloss.NewStyle().Foreground(accent).Bold(true)
	styleKey = lipgloss.NewStyle().Foreground(accent).Bold(true)
	styleHint = lipgloss.NewStyle().Foreground(textFaint).Italic(true)

	// Allocated once: diff/error styling runs per output line.
	styleDiffAdd = lipgloss.NewStyle().Foreground(safe)
	styleDiffDel = lipgloss.NewStyle().Foreground(danger)

	// Islands: one rounded border per zone, accent when focused.
	styleIsland = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1)
	styleIslandActive = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(0, 1)

	styleConfirmDanger = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(danger).
		Padding(1, 2)
	// The confirm modal is always focused while visible, so its resting
	// border is accent — danger overrides it, never the reverse.
	styleConfirmAccent = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(1, 2)

	tree.RefreshStyles()
	status.RefreshStyles()
	render.RefreshStyles()
	welcome.RefreshStyles()
}

// Unknown scope renders neutral, never safe-looking.
func mutabilityStyle(m string) lipgloss.Style {
	switch m {
	case "likely_irreversible":
		return styleDanger
	case "system_affecting":
		return styleCaution
	case "writes_workspace":
		return styleMuted
	case "read_only":
		return styleSafe
	default:
		return styleMuted
	}
}
