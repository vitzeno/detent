// Package island wraps a zone (title and lines) in a rounded border of
// the caller's colour. Every line is truncated to the inner width with a
// visible …, since a wrapped line would push the session bar off the top.
package island

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Inner is the content width of an island width wide: border and
// padding take two cells a side, and width never goes below 10.
func Inner(width int) int { return max(10, width) - 4 }

// Render wraps lines in a bordered island height tall and width wide,
// padding or truncating to fit. An empty title renders none. A line
// holding newlines counts as several, and height is at least one.
func Render(title string, border color.Color, lines []string, width, height int) string {
	width = max(10, width)
	height = max(1, height)
	inner := Inner(width)
	body := make([]string, 0, height)
	if title != "" {
		body = append(body, fitLine(strings.ReplaceAll(title, "\n", " "), inner))
	}
	for _, l := range lines {
		for part := range strings.SplitSeq(l, "\n") {
			body = append(body, fitLine(part, inner))
		}
	}
	if len(body) > height {
		body = body[:height]
	}
	for len(body) < height {
		body = append(body, "")
	}
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1)
	// lipgloss v2's Width is the total rendered width, border included.
	return style.Width(width).Render(strings.Join(body, "\n"))
}

// fitLine cuts over-wide lines with an ANSI-aware … tail. lipgloss would
// otherwise wrap them, and a tab it expands after measuring does the same.
func fitLine(s string, n int) string {
	s = strings.NewReplacer("\t", "    ", "\r", "").Replace(s)
	return ansi.Truncate(s, n, "…")
}
