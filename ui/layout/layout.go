// Package layout provides small arrangement primitives: Split divides a
// budget of cells among weighted regions, Row and Column join
// pre-rendered blocks. Rearranging the screen becomes a data change to
// weights, not a sizing-math rewrite.
package layout

import (
	"charm.land/lipgloss/v2"
)

// Split divides total among len(weights) shares proportional to each
// weight, each guaranteed at least min. A share below min gets floored
// to min and removed from the weighted pool; what that took gets
// re-split among what's left, so shares still sum to exactly total as
// long as total >= min*len(weights). Below that, every share still
// gets min and the sum can exceed total.
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
			return 1 // avoid dividing by zero
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

// Row joins pre-rendered, equal-height blocks left to right. Blocks
// must already be the height the caller intended — Row only arranges,
// never pads or truncates.
func Row(blocks ...string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, blocks...)
}

// Truncate cuts s to w columns, marking where it cut. Bytes, not
// display columns: callers pass ASCII-ish paths and commands.
func Truncate(s string, w int) string {
	if w < 4 {
		w = 4
	}
	if len(s) <= w {
		return s
	}
	return s[:w-1] + "…"
}
