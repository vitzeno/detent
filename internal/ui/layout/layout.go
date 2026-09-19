// Package layout provides small, reusable arrangement primitives —
// Split for dividing a budget of cells among weighted regions, Row and
// Column for joining pre-rendered same-size blocks — so rearranging the
// screen (stacked vs. side by side, how much each zone gets) is a data
// change to weights and grouping, not a rewrite of sizing math and
// string-joining spread across the view code.
package layout

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Split divides total among len(weights) shares proportional to each
// weight, each guaranteed at least min. A share that would fall below
// min is floored to min and removed from the weighted pool; whatever
// budget that took is then re-split among what's left, so the shares
// still sum to exactly total as long as total >= min*len(weights) (the
// same precondition sizeViewport already enforces by flooring its own
// available space before calling Split). Below that floor there's no
// more budget to redistribute — every share still gets at least min,
// and the sum can then exceed total.
func Split(total int, weights []int, min int) []int {
	if len(weights) == 0 {
		return nil
	}
	return split(total, weights, min, make([]int, len(weights)), make([]bool, len(weights)))
}

func split(total int, weights []int, min int, out []int, floored []bool) []int {
	n := len(weights)
	sumW, activeN := 0, 0
	weight := func(w int) int {
		if w <= 0 {
			return 1 // degenerate weight: treat as 1 rather than dividing by zero
		}
		return w
	}
	for i, w := range weights {
		if !floored[i] {
			sumW += weight(w)
			activeN++
		}
	}
	if activeN == 0 {
		return out
	}

	tmp, used := make([]int, n), 0
	for i, w := range weights {
		if !floored[i] {
			tmp[i] = total * weight(w) / sumW
			used += tmp[i]
		}
	}
	for i := 0; used < total && i < n; i++ {
		if !floored[i] {
			tmp[i]++
			used++
		}
	}

	newlyFloored := false
	for i := range weights {
		if !floored[i] && tmp[i] < min {
			out[i] = min
			floored[i] = true
			total -= min
			newlyFloored = true
		}
	}
	if newlyFloored {
		return split(total, weights, min, out, floored)
	}
	for i := range weights {
		if !floored[i] {
			out[i] = tmp[i]
		}
	}
	return out
}

// Row joins pre-rendered, equal-height blocks left to right. Each
// block is expected to already be exactly the height the caller
// intended (island.Render guarantees this) — Row only arranges, it
// never pads or truncates.
func Row(blocks ...string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, blocks...)
}

// Column joins pre-rendered blocks top to bottom — the vertical
// counterpart to Row, named the same way so a layout expressed as rows
// of columns (or columns of rows) reads as one consistent vocabulary
// instead of some joins being lipgloss calls and others being raw "\n"
// concatenation.
func Column(blocks ...string) string {
	return strings.Join(blocks, "\n")
}
