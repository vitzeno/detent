// Package layout provides small arrangement primitives: Split divides a
// budget of cells among weighted regions, Row joins pre-rendered blocks
// and Truncate fits a string to one line.
package layout

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Split divides total proportionally to weights, each share at least
// min. A floored share leaves the pool and what it took is re-split,
// so shares sum to total while total >= min*len(weights).
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

// Row joins pre-rendered, equal-height blocks left to right. It only
// arranges, and never pads or truncates.
func Row(blocks ...string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, blocks...)
}

// Truncate fits s into one line of at most w columns, so no tail lands
// outside the caller's frame. It cuts runes, never mid-character.
func Truncate(s string, w int) string {
	if w < 4 {
		w = 4
	}
	s = flatten(s)
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

// flatten collapses a multi-line string into one, indentation and all.
// One already on one line is untouched, its spacing as written.
func flatten(s string) string {
	if strings.IndexFunc(s, isBreak) < 0 {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}

func isBreak(r rune) bool { return r == '\n' || r == '\r' || r == '\v' || r == '\f' }
