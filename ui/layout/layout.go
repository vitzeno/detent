// Package layout provides small arrangement primitives: Split divides a
// budget of cells among weighted regions, Row joins pre-rendered blocks,
// Truncate fits a string to one line and defuses it.
package layout

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vitzeno/detent/termsafe"
)

// Split divides total proportionally to weights, each share at least
// least, and weights <= 0 count as 1. A floored share leaves the pool and
// what it took is re-split, so shares sum to total while total >= least*len(weights).
func Split(total int, weights []int, least int) []int {
	if len(weights) == 0 {
		return nil
	}
	return split(total, weights, least, make([]int, len(weights)), make([]bool, len(weights)))
}

func split(total int, weights []int, least int, out []int, floored []bool) []int {
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
		if !floored[i] && tmp[i] < least {
			out[i] = least
			floored[i] = true
			total -= least
			newlyFloored = true
		}
	}
	if newlyFloored {
		return split(total, weights, least, out, floored)
	}
	for i := range weights {
		if !floored[i] {
			out[i] = tmp[i]
		}
	}
	return out
}

// Row joins pre-rendered, equal-height blocks left to right. It only
// arranges, and never truncates.
func Row(blocks ...string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, blocks...)
}

// MinTruncate is the narrowest Truncate will cut to.
const MinTruncate = 4

// Truncate fits s into one line of at most w cells, so no tail lands
// outside the caller's frame. It cuts graphemes, never mid-character.
func Truncate(s string, w int) string {
	return ansi.Truncate(termsafe.Printable(flatten(s)), max(w, MinTruncate), "…")
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
