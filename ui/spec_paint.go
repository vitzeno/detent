package ui

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vitzeno/detent/viewspec"
)

// painter maps viewspec's display intents onto the active theme. It
// reads the package style vars at call time, so a theme change follows
// without rebinding anything.
type painter struct{}

var _ viewspec.Painter = painter{}

func (painter) Paint(r viewspec.Role, s string) string {
	return roleStyle(r).Render(s)
}

// Width and Truncate are ANSI-aware because Paint emits escapes.
// Measuring painted text with len is how a table looks aligned in
// tests and ragged in a terminal.
func (painter) Width(s string) int { return lipgloss.Width(s) }

func (painter) Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return ansi.Truncate(s, n, "…")
}

// RoleHeading and RoleAccent render alike under every shipped theme.
// They stay separate intents: a header is not a selection, and a theme
// is free to tell them apart.
func roleStyle(r viewspec.Role) lipgloss.Style {
	switch r {
	case viewspec.RoleMuted:
		return styleMuted
	case viewspec.RoleFaint:
		return styleFaint
	case viewspec.RoleHeading:
		return styleBrand
	case viewspec.RoleAccent:
		return styleRowCursor
	case viewspec.RoleSafe:
		return styleSafe
	case viewspec.RoleCaution:
		return styleCaution
	case viewspec.RoleDanger:
		return styleDanger
	default:
		return styleGoal
	}
}
