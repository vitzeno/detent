package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/status"
	"github.com/vitzeno/detent/ui/theme"
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

	// Allocated once: diff/error styling runs per output line.

	// Islands: one rounded border per zone, accent when focused.

	// The confirm modal is always focused while visible, so its resting
	// border is accent — danger overrides it, never the reverse.

	status.RefreshStyles()
	welcome.RefreshStyles()
}
