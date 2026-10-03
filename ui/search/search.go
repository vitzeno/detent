// Package search ranks text against what the human typed into the finder.
// Short text is matched fuzzily, long text literally a line at a time.
package search

import (
	"strings"
	"unicode"
)

// Hit is one match: higher scores rank first, and Pos are the matched rune
// offsets in the text, ascending, for highlighting.
type Hit struct {
	Score int
	Pos   []int
}

// maxRunes bounds what Fuzzy reads of one text. A command past it is a
// heredoc, and its head is what anyone remembers.
const maxRunes = 2048

// Scoring, after fzf's first algorithm: a match is worth more at a word's
// start or a camel hump, and in a run, and less across a gap.
const (
	scoreMatch       = 16
	bonusBoundary    = 8
	bonusCamel       = 7
	bonusConsecutive = 4
	bonusFirstRune   = 2 // multiplies the first query rune's bonus
	penaltyGapStart  = 3
	penaltyGapExtend = 1
)

// Fuzzy reports whether every space-separated term of query appears in text
// in order, though not necessarily together. A query with an upper-case
// letter matches case exactly, one without ignores it.
func Fuzzy(query, text string) (Hit, bool) {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return Hit{}, true
	}
	exact := hasUpper(query)
	runes := []rune(text)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	var hit Hit
	for _, term := range terms {
		h, ok := fuzzyTerm([]rune(term), runes, exact)
		if !ok {
			return Hit{}, false
		}
		hit.Score += h.Score
		hit.Pos = merge(hit.Pos, h.Pos)
	}
	return hit, true
}

// Lines finds the first line of text holding every term of query literally,
// and returns its index with the matched offsets in that line.
func Lines(query, text string) (int, Hit, bool) {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return 0, Hit{}, false
	}
	exact := hasUpper(query)
	for i, line := range strings.Split(text, "\n") {
		if h, ok := literal(terms, line, exact); ok {
			return i, h, true
		}
	}
	return 0, Hit{}, false
}

// fuzzyTerm finds the tightest window holding term: the earliest end going
// forward, then the latest start going back from there.
func fuzzyTerm(term, text []rune, exact bool) (Hit, bool) {
	ti, end := 0, -1
	for i, r := range text {
		if same(term[ti], r, exact) {
			ti++
			if ti == len(term) {
				end = i
				break
			}
		}
	}
	if end < 0 {
		return Hit{}, false
	}
	pos := make([]int, len(term))
	ti = len(term) - 1
	for i := end; i >= 0 && ti >= 0; i-- {
		if same(term[ti], text[i], exact) {
			pos[ti] = i
			ti--
		}
	}
	return Hit{Score: score(text, pos), Pos: pos}, true
}

func score(text []rune, pos []int) int {
	total := 0
	for k, p := range pos {
		bonus := bonusAt(text, p)
		s := scoreMatch + bonus
		switch {
		case k == 0:
			s += bonus * (bonusFirstRune - 1)
		case p == pos[k-1]+1:
			s += bonusConsecutive
		default:
			s -= penaltyGapStart + (p-pos[k-1]-2)*penaltyGapExtend
		}
		total += s
	}
	return total
}

// bonusAt is what matching at p is worth beyond the match itself.
func bonusAt(text []rune, p int) int {
	if p == 0 {
		return bonusBoundary
	}
	prev, cur := text[p-1], text[p]
	switch {
	case !unicode.IsLetter(prev) && !unicode.IsDigit(prev):
		return bonusBoundary
	case unicode.IsLower(prev) && unicode.IsUpper(cur):
		return bonusCamel
	}
	return 0
}

// literal matches each term as a run of line's runes, compared a rune at a
// time so the offsets stay true whatever lower-casing does to a rune's width.
func literal(terms []string, line string, exact bool) (Hit, bool) {
	hay := []rune(line)
	var hit Hit
	for _, term := range terms {
		needle := []rune(term)
		at := index(hay, needle, exact)
		if at < 0 {
			return Hit{}, false
		}
		for i := range needle {
			hit.Pos = merge(hit.Pos, []int{at + i})
		}
		hit.Score += len(needle) * scoreMatch
	}
	return hit, true
}

func index(hay, needle []rune, exact bool) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		k := 0
		for k < len(needle) && same(needle[k], hay[i+k], exact) {
			k++
		}
		if k == len(needle) {
			return i
		}
	}
	return -1
}

func same(q, r rune, exact bool) bool {
	if exact {
		return q == r
	}
	return unicode.ToLower(q) == unicode.ToLower(r)
}

func hasUpper(s string) bool { return strings.IndexFunc(s, unicode.IsUpper) >= 0 }

// merge unions two ascending offset lists.
func merge(a, b []int) []int {
	out := make([]int, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case j == len(b) || (i < len(a) && a[i] < b[j]):
			out = append(out, a[i])
			i++
		case i == len(a) || b[j] < a[i]:
			out = append(out, b[j])
			j++
		default:
			out = append(out, a[i])
			i, j = i+1, j+1
		}
	}
	return out
}
