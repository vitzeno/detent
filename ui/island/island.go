// Package island wraps a zone (title + lines) in a rounded border,
// accent when focused. One place owns padding and sizing so every zone
// tiles the terminal identically. Every line is truncated to the inner
// width: a wrapped line would render as two physical lines and push the
// session bar off the top, so the island guarantees one line in, one
// line out — with a visible … where it cuts.
package island

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/vitzeno/detent/ui/theme"
)

// Render wraps title and lines in a bordered island height content
// lines tall (title included) and width columns wide. Short content
// pads with blanks; long content truncates. An empty title renders no
// title line and all height lines stay content.
func Render(title string, active bool, lines []string, width, height int) string {
	width = max(10, width)
	inner := width - 4 // border plus padding on both sides
	body := make([]string, 0, height)
	if title != "" {
		body = append(body, fitLine(title, inner))
	}
	for _, l := range lines {
		body = append(body, fitLine(l, inner))
	}
	switch {
	case len(body) > height:
		body = body[:height]
	default:
		for len(body) < height {
			body = append(body, "")
		}
	}
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Border).
		Padding(0, 1)
	if active {
		style = style.BorderForeground(theme.Accent)
	}
	return style.Width(width - 2).Render(strings.Join(body, "\n"))
}

// fitLine cuts over-wide lines with an ANSI-aware … tail. lipgloss
// would otherwise wrap them and break the frame's line budget.
func fitLine(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	return ansi.Truncate(s, n, "…")
}
