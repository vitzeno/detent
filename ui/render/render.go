// Package render colours a unified diff for the save confirm. The
// output pane's own transforms moved to viewspec; what is left is what
// draws outside it.
package render

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/theme"
)

var (
	faint   lipgloss.Style
	muted   lipgloss.Style
	added   lipgloss.Style
	removed lipgloss.Style
)

func init() { RefreshStyles() }

// RefreshStyles rebuilds this package's styles from the current
// theme — call after theme.Apply.
func RefreshStyles() {
	faint = lipgloss.NewStyle().Foreground(theme.TextFaint)
	muted = lipgloss.NewStyle().Foreground(theme.TextMuted)
	added = lipgloss.NewStyle().Foreground(theme.Safe)
	removed = lipgloss.NewStyle().Foreground(theme.Danger)
}

// DiffClass classifies one unified-diff line: add, del, hunk, meta,
// ctx. Split from DiffLine so it tests without a colour profile.
func DiffClass(l string) string {
	switch {
	case strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
		return "add"
	case strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---"):
		return "del"
	case strings.HasPrefix(l, "@@"):
		return "hunk"
	case strings.HasPrefix(l, "diff ") || strings.HasPrefix(l, "index ") ||
		strings.HasPrefix(l, "---") || strings.HasPrefix(l, "+++"):
		return "meta"
	default:
		return "ctx"
	}
}

// DiffLine colors unified-diff lines: green added, red removed,
// faint hunk headers and metadata.
func DiffLine(l string) string {
	switch DiffClass(l) {
	case "add":
		return added.Render(l)
	case "del":
		return removed.Render(l)
	case "hunk":
		return faint.Render(l)
	case "meta":
		return muted.Render(l)
	default:
		return l
	}
}
