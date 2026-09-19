// Package theme owns the shared palette. Component subpackages build
// their own styles from these colors so one hex change re-skins every
// component without touching their code.
package theme

import "github.com/charmbracelet/lipgloss"

var (
	Accent    = lipgloss.Color("#45D6C4")
	AccentDim = lipgloss.Color("#2A8578")

	Safe    = lipgloss.Color("#6FD98C")
	Caution = lipgloss.Color("#E8B24D")
	Danger  = lipgloss.Color("#F0665E")

	TextPrimary = lipgloss.AdaptiveColor{Light: "#1D2129", Dark: "#E7ECF3"}
	TextMuted   = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8992A8"}
	TextFaint   = lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#5C6478"}
	Border      = lipgloss.AdaptiveColor{Light: "#D6D9E0", Dark: "#333A4D"}
)
