// Package ui is a full-screen dynamic TUI.
package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/vitzeno/detent/internal/ui/theme"
)

var (
	accent    = theme.Accent
	accentDim = theme.AccentDim

	safe    = theme.Safe
	caution = theme.Caution
	danger  = theme.Danger

	textPrimary = theme.TextPrimary
	textMuted   = theme.TextMuted
	textFaint   = theme.TextFaint
	border      = theme.Border
)

var (
	styleBrand = lipgloss.NewStyle().Foreground(accent).Bold(true)
	styleGoal  = lipgloss.NewStyle().Foreground(textPrimary)
	styleMuted = lipgloss.NewStyle().Foreground(textMuted)
	styleFaint = lipgloss.NewStyle().Foreground(textFaint)

	styleSafe    = lipgloss.NewStyle().Foreground(safe)
	styleCaution = lipgloss.NewStyle().Foreground(caution).Bold(true)
	styleDanger  = lipgloss.NewStyle().Foreground(danger).Bold(true)

	styleRowCursor = lipgloss.NewStyle().Foreground(accent).Bold(true)
	styleKey       = lipgloss.NewStyle().Foreground(accent).Bold(true)
	styleHint      = lipgloss.NewStyle().Foreground(textFaint).Italic(true)

	styleConfirmBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Padding(1, 2)
	styleConfirmDanger = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(danger).
				Padding(1, 2)

	styleStderr = lipgloss.NewStyle().Foreground(caution)
)

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
