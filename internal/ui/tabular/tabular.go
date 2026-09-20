// Package tabular renders whitespace-aligned command output (ps, df)
// as a real table component. Jev's table kind decides WHEN; everything
// here is deterministic code. Parse reports ok=false when the shape
// isn't tabular and the caller falls back to the plain log viewport —
// a wrong component is worse than a plain one.
package tabular

import (
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"

	"github.com/vitzeno/detent/internal/ui/theme"
)

// Parse splits whitespace-aligned output into columns and rows. The
// first line is the header; trailing fields join into the last column,
// which is where commands like ps put free text (COMMAND). Reports
// ok=false unless at least two lines share a header of two or more
// fields.
func Parse(output string, width int) (columns []table.Column, rows []table.Row, ok bool) {
	var grid [][]string
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		grid = append(grid, strings.Fields(line))
	}
	if len(grid) < 2 || len(grid[0]) < 2 {
		return nil, nil, false
	}
	n := len(grid[0])
	joined := make([][]string, len(grid))
	for i, fields := range grid {
		if len(fields) < n {
			return nil, nil, false
		}
		row := make([]string, n)
		copy(row, fields[:n-1])
		row[n-1] = strings.Join(fields[n-1:], " ")
		joined[i] = row
	}
	grid = joined

	widths := fit(grid, max(20, width-6))
	columns = make([]table.Column, n)
	for i, h := range grid[0] {
		columns[i] = table.Column{Title: h, Width: widths[i]}
	}
	for _, fields := range grid[1:] {
		rows = append(rows, table.Row(fields))
	}
	return columns, rows, true
}

// fit shares width across columns proportional to their widest cell, so
// one long column can't starve the rest. Never scrolls horizontally:
// cells truncate with … instead.
func fit(grid [][]string, total int) []int {
	n := len(grid[0])
	maxw := make([]int, n)
	sum := 0
	for _, fields := range grid {
		for i, f := range fields {
			if w := lipgloss.Width(f); w > maxw[i] {
				maxw[i] = w
			}
		}
	}
	for _, w := range maxw {
		sum += w
	}
	widths := make([]int, n)
	if sum <= total {
		copy(widths, maxw)
		return widths
	}
	// Proportional share, minimum 4 each so every column stays visible.
	rest := total
	for i, w := range maxw {
		widths[i] = max(4, w*total/sum)
		rest -= widths[i]
	}
	for i := 0; rest < 0; i, rest = (i+1)%n, rest+1 {
		if widths[i] > 4 {
			widths[i]--
		}
	}
	return widths
}

// Styles is the one table look: teal header over a full-width rule,
// faint vertical rules between columns, bold selected row. Cell
// borders stop at right-only deliberately: bubbles/table borders each
// cell independently rather than drawing the grid as one structure, so
// combining a right border with a bottom border draws a stray corner
// glyph at every cell join instead of a clean crossing — a bottom-only
// border (as Header uses) joins seamlessly into one line, but adding a
// right border to that same cell breaks it. Right-only avoids the
// artifact while still reading as a grid alongside the header's rule.
func Styles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.Foreground(theme.Accent).Bold(true).
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(theme.Border)
	s.Cell = s.Cell.
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(theme.Border)
	s.Selected = s.Selected.Foreground(theme.Accent).Bold(true)
	return s
}

// Build assembles the component: columns, rows, cursor, and height.
func Build(columns []table.Column, rows []table.Row, cursor, height int, focused bool) table.Model {
	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithHeight(height),
		table.WithFocused(focused),
	)
	t.SetStyles(Styles())
	t.SetCursor(cursor)
	return t
}
