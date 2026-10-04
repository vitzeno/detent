package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vitzeno/detent/viewspec"
)

// painter maps viewspec's display intents onto the active theme, read
// at call time so a theme change needs no rebinding.
type painter struct{}

var _ viewspec.Painter = painter{}

func (painter) Paint(r viewspec.Role, s string) string {
	return roleStyle(r).Render(s)
}

// Width and Truncate are ANSI-aware because Paint emits escapes, and
// measuring painted text with len leaves a table ragged in a terminal.
func (painter) Width(s string) int { return lipgloss.Width(s) }

func (painter) Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return ansi.Truncate(s, n, "…")
}

// Wrap breaks at spaces, and mid-word only for a word wider than n, keeping
// a command's colours on whichever line they fall.
func (painter) Wrap(s string, n int) []string {
	if n <= 0 || ansi.StringWidth(s) <= n {
		return []string{s}
	}
	return strings.Split(ansi.Wrap(s, n, ""), "\n")
}

// roleStyle maps a role to its style. RoleHeading and RoleAccent render
// alike today but stay separate, so a theme is free to tell them apart.
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
