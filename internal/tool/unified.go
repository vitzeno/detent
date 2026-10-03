package tool

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aymanbagabas/go-udiff/lcs"
)

// diffContext is diff -u's lines of context around each change.
const diffContext = 3

// changeShown is showDiff in Go: what changed in p, windowed the same way.
// Like read_file it drops each line's \r, which the sandbox's awk keeps.
func changeShown(p, before, after string) string {
	label := p
	if path(p) != quote(p) {
		label = "./" + p
	}
	out, _ := windowLines(strings.NewReader(unifiedDiff(label, before, after)), 1, diffWindow, diffMore, diffEmpty)
	return out // a strings.Reader cannot fail
}

// unifiedDiff is diff -u -L label -L label, or "" for no change. go-udiff finds
// what changed, then GNU diff's own rules place each change and print hunks.
func unifiedDiff(label, before, after string) string {
	if before == after {
		return ""
	}
	if strings.Contains(before, "\x00") || strings.Contains(after, "\x00") {
		return fmt.Sprintf("Binary files %s and %s differ\n", label, label)
	}
	a, b := splitKeep(before), splitKeep(after)
	// Each flag slice has a false sentinel either side, as GNU diff's has.
	ca, cb := make([]bool, len(a)+2), make([]bool, len(b)+2)
	for _, d := range lcs.DiffLines(a, b) {
		for k := d.Start; k < d.End; k++ {
			ca[k+1] = true
		}
		for k := d.ReplStart; k < d.ReplEnd; k++ {
			cb[k+1] = true
		}
	}
	shiftBoundaries(a, ca, cb)
	shiftBoundaries(b, cb, ca)

	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", label, label)
	changes := changeRuns(len(a), len(b), ca, cb)
	for len(changes) > 0 {
		n := 1
		for n < len(changes) && changes[n].a-(changes[n-1].a+changes[n-1].del) <= 2*diffContext {
			n++
		}
		writeHunk(&out, a, b, changes[:n])
		changes = changes[n:]
	}
	return out.String()
}

// change is one run of lines deleted from a and inserted from b.
type change struct{ a, b, del, ins int }

func changeRuns(na, nb int, ca, cb []bool) []change {
	var cs []change
	for i, j := 0, 0; i < na || j < nb; {
		if !ca[i+1] && !cb[j+1] {
			i, j = i+1, j+1
			continue
		}
		c := change{a: i, b: j}
		for ca[i+1] {
			i++
		}
		for cb[j+1] {
			j++
		}
		c.del, c.ins = i-c.a, j-c.b
		cs = append(cs, c)
	}
	return cs
}

func writeHunk(out *strings.Builder, a, b []string, cs []change) {
	first, last := cs[0], cs[len(cs)-1]
	lo := max(first.a-diffContext, 0)
	hi := min(last.a+last.del+diffContext, len(a))
	blo := first.b - (first.a - lo)
	bhi := last.b + last.ins + (hi - last.a - last.del)
	fmt.Fprintf(out, "@@ -%s +%s @@\n", hunkRange(lo, hi), hunkRange(blo, bhi))
	i := lo
	for _, c := range cs {
		for ; i < c.a; i++ {
			diffLine(out, ' ', a[i])
		}
		for k := c.a; k < c.a+c.del; k++ {
			diffLine(out, '-', a[k])
		}
		for k := c.b; k < c.b+c.ins; k++ {
			diffLine(out, '+', b[k])
		}
		i = c.a + c.del
	}
	for ; i < hi; i++ {
		diffLine(out, ' ', a[i])
	}
}

// hunkRange is diff's: an empty range names the line before it.
func hunkRange(lo, hi int) string {
	switch hi - lo {
	case 0:
		return fmt.Sprintf("%d,0", lo)
	case 1:
		return strconv.Itoa(lo + 1)
	}
	return fmt.Sprintf("%d,%d", lo+1, hi-lo)
}

func diffLine(out *strings.Builder, mark byte, line string) {
	out.WriteByte(mark)
	out.WriteString(line)
	if !strings.HasSuffix(line, "\n") {
		out.WriteString("\n\\ No newline at end of file\n")
	}
}

// splitKeep splits s into lines that keep their newline, so a last line
// without one differs from the same text with one, as it does to diff.
func splitKeep(s string) []string {
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// shiftBoundaries is GNU diff's: a run of changes that could sit in several
// places slides down as far as it can, unless that parts it from its partner.
func shiftBoundaries(lines []string, changed, other []bool) {
	at := func(k int) bool { return changed[k+1] }
	set := func(k int, v bool) { changed[k+1] = v }
	oat := func(k int) bool { return other[k+1] }
	end := len(lines)
	for i, j := 0, 0; ; {
		for i < end && !at(i) {
			for oat(j) {
				j++
			}
			j++
			i++
		}
		if i == end {
			return
		}
		start := i
		for i++; at(i); i++ {
		}
		for oat(j) {
			j++
		}
		var corresponding int
		for {
			run := i - start
			for start > 0 && lines[start-1] == lines[i-1] {
				start--
				set(start, true)
				i--
				set(i, false)
				for at(start - 1) {
					start--
				}
				for j--; oat(j); j-- {
				}
			}
			corresponding = end
			if oat(j - 1) {
				corresponding = i
			}
			for i != end && lines[start] == lines[i] {
				set(start, false)
				start++
				set(i, true)
				i++
				for at(i) {
					i++
				}
				for j++; oat(j); j++ {
					corresponding = i
				}
			}
			if run == i-start {
				break
			}
		}
		for corresponding < i {
			start--
			set(start, true)
			i--
			set(i, false)
			for j--; oat(j); j-- {
			}
		}
	}
}
