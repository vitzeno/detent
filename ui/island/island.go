// Package island wraps a zone (title + lines) in a rounded border of
// the caller's colour. One place owns padding and sizing so every zone
// tiles the terminal identically. Every line is truncated to the inner
// width: a wrapped line would render as two physical lines and push the
// session bar off the top, so the island guarantees one line in, one
// line out — with a visible … where it cuts.
package island

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Render wraps lines in a bordered island height tall and width wide,
// padding or truncating to fit. An empty title renders none.
func Render(title string, border color.Color, lines []string, width, height int) string {
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
		BorderForeground(border).
		Padding(0, 1)
	// lipgloss v2's Width is the total rendered width, border included —
	// under v1 it was the content width and the border sat outside it.
	return style.Width(width).Render(strings.Join(body, "\n"))
}

// fitLine cuts over-wide lines with an ANSI-aware … tail. lipgloss
// would otherwise wrap them and break the frame's line budget.
func fitLine(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	return ansi.Truncate(s, n, "…")
}
