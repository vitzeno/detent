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

// toolName is how a tool's name stands out in history, one colour a kind.
var toolName = bakeToolNames(palette)

// styleCommand is a command awaiting approval, on a panel of its own.
var styleCommand = bakeCommand(palette)

// RefreshStyles rebuilds every style baked from a theme color, here and in each
// subpackage. Call it after theme.Apply and before the program runs.
func RefreshStyles() {
	palette = theme.Current()
	styleBrand, styleGoal, styleMuted, styleFaint,
		styleSafe, styleCaution, styleDanger, styleRowCursor = bake(palette)
	toolName = bakeToolNames(palette)
	styleCommand = bakeCommand(palette)

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

func bakeCommand(p theme.Theme) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(p.TextPrimary).Background(p.Border)
}

// toolNames colour a tool's name by what it does: the shell, reading, writing,
// the web, a server's tool, a subagent and anything else, plus a command's program.
type toolNames struct {
	shell, read, write, web, server, agent, other, program lipgloss.Style
}

func bakeToolNames(p theme.Theme) toolNames {
	label := func(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c).Bold(true) }
	return toolNames{
		shell: label(p.Tools.Shell), read: label(p.Tools.Read), write: label(p.Tools.Write),
		web: label(p.Tools.Web), server: label(p.Tools.Server), agent: label(p.Tools.Agent),
		other:   label(p.Tools.Other),
		program: lipgloss.NewStyle().Foreground(p.TextPrimary).Bold(true),
	}
}
