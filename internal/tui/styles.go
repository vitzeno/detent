// Package tui is the Bubble Tea front end (PLAN.md §8, §9) over the loop
// built in steps 1-8. "Jev output never reaches the screen" — this
// package is the only thing standing between the model's typed answers
// and what a human reads; everything shown is a format string over
// values the code already holds (§8).
package tui

import "github.com/charmbracelet/lipgloss"

// Palette. accent is the one color that carries the app's own identity —
// a precise, mechanical teal, not the purple-gradient/terracotta looks
// that read as generic AI-tool design. danger/caution/safe are semantic,
// not decorative: they exist because §7's danger tiers are the single
// most load-bearing fact in this whole product, and the UI should never
// let a destructive step look like a safe one.
var (
	accent    = lipgloss.Color("#45D6C4")
	accentDim = lipgloss.Color("#2A8578")

	safe    = lipgloss.Color("#6FD98C")
	caution = lipgloss.Color("#E8B24D")
	danger  = lipgloss.Color("#F0665E")

	textPrimary = lipgloss.AdaptiveColor{Light: "#1D2129", Dark: "#E7ECF3"}
	textMuted   = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8992A8"}
	textFaint   = lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#5C6478"}
	border      = lipgloss.AdaptiveColor{Light: "#D6D9E0", Dark: "#333A4D"}
	borderFocus = accent
)

// Type scale + roles. One accent weight (bold), one muted weight (faint),
// applied consistently rather than ad hoc per view.
var (
	styleGoal   = lipgloss.NewStyle().Foreground(textPrimary)
	styleMuted  = lipgloss.NewStyle().Foreground(textMuted)
	styleFaint  = lipgloss.NewStyle().Foreground(textFaint)
	styleAccent = lipgloss.NewStyle().Foreground(accent).Bold(true)

	styleSafe    = lipgloss.NewStyle().Foreground(safe)
	styleCaution = lipgloss.NewStyle().Foreground(caution).Bold(true)
	styleDanger  = lipgloss.NewStyle().Foreground(danger).Bold(true)

	styleStepDone    = lipgloss.NewStyle().Foreground(safe)
	styleStepPending = lipgloss.NewStyle().Foreground(accent)
	styleStepFuture  = lipgloss.NewStyle().Foreground(textFaint)

	stylePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Padding(1, 2)

	stylePanelDanger = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(danger).
				Padding(1, 2)

	styleInputBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Padding(0, 1)

	styleInputBoxFocus = styleInputBox.BorderForeground(borderFocus)

	styleHint = lipgloss.NewStyle().Foreground(textFaint).Italic(true)

	styleKey = lipgloss.NewStyle().Foreground(accent).Bold(true)
)

// dangerStyle picks the semantic color for a danger tier's own label —
// used everywhere a tier appears (the confirm panel, a step's badge)
// so "destructive" always reads the same regardless of context.
func dangerStyle(tier string) lipgloss.Style {
	switch tier {
	case "destructive":
		return styleDanger
	case "caution":
		return styleCaution
	default:
		return styleSafe
	}
}
