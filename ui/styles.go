package ui

import (
	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/status"
	"github.com/vitzeno/detent/ui/theme"
	"github.com/vitzeno/detent/ui/welcome"
)

// palette is the theme the styles below were baked from.
var palette = theme.Current()

var styleBrand, styleGoal, styleMuted, styleFaint,
	styleSafe, styleCaution, styleDanger, styleRowCursor = bake(palette)

// RefreshStyles rebuilds every style baked from a theme color, here and in each
// subpackage. Call it after theme.Apply and before the program runs.
func RefreshStyles() {
	palette = theme.Current()
	styleBrand, styleGoal, styleMuted, styleFaint,
		styleSafe, styleCaution, styleDanger, styleRowCursor = bake(palette)

	status.RefreshStyles()
	welcome.RefreshStyles()
}

func bake(p theme.Theme) (brand, goal, muted, faint, safe, caution, danger, rowCursor lipgloss.Style) {
	return lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		lipgloss.NewStyle().Foreground(p.TextPrimary),
		lipgloss.NewStyle().Foreground(p.TextMuted),
		lipgloss.NewStyle().Foreground(p.TextFaint),
		lipgloss.NewStyle().Foreground(p.Safe),
		lipgloss.NewStyle().Foreground(p.Caution).Bold(true),
		lipgloss.NewStyle().Foreground(p.Danger).Bold(true),
		lipgloss.NewStyle().Foreground(p.Accent).Bold(true)
}
