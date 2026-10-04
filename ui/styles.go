package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/status"
	"github.com/vitzeno/detent/ui/theme"
	"github.com/vitzeno/detent/ui/welcome"
)

// palette is the theme the styles below were baked from.
var palette = theme.Current()

var styleBrand, styleGoal, styleMuted, styleFaint,
	styleSafe, styleCaution, styleDanger, styleRowCursor = bake(palette)

// pill is how a tool's name stands out in history, one colour a kind.
var pill = bakePills(palette)

// RefreshStyles rebuilds every style baked from a theme color, here and in each
// subpackage. Call it after theme.Apply and before the program runs.
func RefreshStyles() {
	palette = theme.Current()
	styleBrand, styleGoal, styleMuted, styleFaint,
		styleSafe, styleCaution, styleDanger, styleRowCursor = bake(palette)
	pill = bakePills(palette)

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

// pills are the tool-name labels: the shell, reading, writing, the web, a
// server's tool and anything else, plus the bold a command's program gets.
type pills struct {
	shell, read, write, web, server, other, program lipgloss.Style
}

func bakePills(p theme.Theme) pills {
	label := func(c color.Color) lipgloss.Style {
		return lipgloss.NewStyle().Background(c).Foreground(p.Background).Bold(true).Padding(0, 1)
	}
	return pills{
		shell: label(p.TextPrimary), read: label(p.Accent), write: label(p.Caution), web: label(p.Safe),
		server: label(p.Danger), other: label(p.TextMuted),
		program: lipgloss.NewStyle().Foreground(p.TextPrimary).Bold(true),
	}
}
