// Package ui is a full-screen dynamic TUI.
package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/vitzeno/detent/internal/ui/theme"
)

var (
	accent = theme.Accent

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
