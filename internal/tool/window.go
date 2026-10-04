package tool

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	// outputBudget keeps a windowed result under capture.MaxOutputBytes, so
	// its footer is never what the capture cuts off.
	outputBudget = 7 * 1024
	// readChunk is how much of a line is read at a time.
	readChunk = 64 * 1024
)

// window prints limit lines from start, stopping early at outputBudget bytes. more
// gets the count left and the line to resume from, empty is for no input.
func window(start, limit int, more, empty string) string {
	return windowSized(start, limit, outputBudget, more, empty)
}

// windowSized is window with its own byte budget, for a tool allowed more.
func windowSized(start, limit, budget int, more, empty string) string {
	return fmt.Sprintf("awk -v s=%d -v n=%d -v b=%d -v more=%s -v empty=%s %s",
		start, limit, budget, quote(awkString(more)+`\n`), quote(awkString(empty)+`\n`), quote(windowScript))
}

// windowScript is window's awk program, which windower mirrors in Go.
const windowScript = `NR < s { next }
!stop && NR >= s + n { stop = NR }
!stop && used > 0 && used + length($0) + 1 > b { stop = NR }
stop { next }
{ line = length($0) > b ? substr($0, 1, b) " [line cut]" : $0; print line; used += length(line) + 1 }
END {
  if (stop) printf more, NR - stop + 1, stop
  else if (NR < s) printf empty, NR
}`

// awkString escapes the backslashes awk -v would otherwise read as escapes.
func awkString(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }

// windowLines is window's awk script in Go, holding at most a budget's worth of
// any line however long, and stopping when ctx does.
func windowLines(ctx context.Context, r io.Reader, start, limit int, more, empty string) (string, error) {
	return windowLinesSized(ctx, r, start, limit, outputBudget, more, empty)
}

// windowLinesSized is windowLines with its own byte budget, as windowSized.
func windowLinesSized(ctx context.Context, r io.Reader, start, limit, budget int, more, empty string) (string, error) {
	w := windower{start: start, limit: limit, budget: budget}
	// As much of one line as the window can need: the budget, a \r and a byte to tell it is over.
	lineKeep := budget + 2
	br := bufio.NewReaderSize(r, readChunk)
	var line []byte
	size := 0
	for {
		if err := ctx.Err(); err != nil {
			return w.b.String(), err
		}
		chunk, err := br.ReadSlice('\n')
		if w.wants() && len(line) < lineKeep {
			line = append(line, chunk[:min(len(chunk), lineKeep-len(line))]...)
		}
		size += len(chunk)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return w.b.String(), err
		}
		if size > 0 {
			whole := size <= lineKeep
			if whole {
				line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
			}
			w.add(string(line), !whole)
		}
		line, size = line[:0], 0
		if err != nil {
			return w.end(more, empty), nil
		}
	}
}

// windower is window's awk script in Go, fed a line at a time.
type windower struct {
	b             strings.Builder
	start, limit  int
	n, used, stop int
	// budget is the bytes it may show, outputBudget when zero.
	budget int
}

// wants is whether the next line could be shown, so a reader knows
// whether to keep it or only count it.
func (w *windower) wants() bool {
	k := w.n + 1
	return w.stop == 0 && k >= w.start && k < w.start+w.limit
}

// add takes the next line, long when line is only its start.
func (w *windower) add(line string, long bool) {
	w.n++
	switch {
	case w.n < w.start || w.stop > 0:
		return
	case w.n >= w.start+w.limit:
		w.stop = w.n
		return
	case w.used > 0 && (long || w.used+len(line)+1 > w.bytes()):
		w.stop = w.n
		return
	}
	if long || len(line) > w.bytes() {
		line = cutRunes(line, w.bytes()) + " [line cut]"
	}
	w.b.WriteString(line)
	w.b.WriteByte('\n')
	w.used += len(line) + 1
}

func (w *windower) bytes() int { return cmp.Or(w.budget, outputBudget) }

// end adds the footer: more when the window stopped early, empty when it never began.
func (w *windower) end(more, empty string) string {
	switch {
	case w.stop > 0:
		w.b.WriteString(footer(more, w.n-w.stop+1, w.stop))
	case w.n < w.start:
		w.b.WriteString(footer(empty, w.n))
	}
	return w.b.String()
}

// windowTop windows lines, the first of total in order, as window would
// all of them: top keeps only lines a window could show.
func windowTop(lines []string, total, limit int, more, empty string) string {
	if total == 0 {
		return footer(empty, 0)
	}
	w := windower{start: 1, limit: limit}
	for _, l := range lines {
		pieces := strings.Split(l, "\n")
		total += len(pieces) - 1
		for _, p := range pieces {
			w.add(strings.TrimSuffix(p, "\r"), false)
		}
	}
	if total > w.n && w.stop == 0 {
		w.stop = w.n + 1
	}
	w.n = total
	return w.end(more, empty)
}

// windowOf windows lines as window would their output, one per line.
func windowOf(lines []string, limit int, more, empty string) string {
	return windowTop(lines, len(lines), limit, more, empty)
}

// top keeps the first items in order that a window could show and counts
// the rest, so a search holds a window's worth however much it finds.
type top[T any] struct {
	cmp           func(a, b T) int
	size          func(T) int
	limit, budget int
	kept          []T
	bytes, total  int
}

// add counts v and keeps it while a window could still show it.
func (t *top[T]) add(v T) {
	t.total++
	i, _ := slices.BinarySearchFunc(t.kept, v, t.cmp)
	if i == len(t.kept) && t.full() {
		return
	}
	t.kept = slices.Insert(t.kept, i, v)
	t.bytes += t.sizeOf(v)
	for len(t.kept) > t.limit || len(t.kept) > 1 && t.over(t.bytes-t.sizeOf(t.kept[len(t.kept)-1])) {
		t.bytes -= t.sizeOf(t.kept[len(t.kept)-1])
		t.kept = t.kept[:len(t.kept)-1]
	}
}

// merge adds what u kept and counts what it did not.
func (t *top[T]) merge(u *top[T]) {
	for _, v := range u.kept {
		t.add(v)
	}
	t.total += u.total - len(u.kept)
}

// full is whether anything after the last kept item would be dropped.
func (t *top[T]) full() bool { return len(t.kept) >= t.limit || t.over(t.bytes) }

// over is whether lines taking this much leave no room for another.
func (t *top[T]) over(used int) bool { return t.budget > 0 && used > t.budget }

func (t *top[T]) sizeOf(v T) int {
	if t.size == nil {
		return 0
	}
	return t.size(v)
}

// topLines keeps lines in cmp's order for a window of limit lines.
func topLines(limit int, cmp func(a, b string) int) *top[string] {
	return &top[string]{cmp: cmp, size: lineSize, limit: limit, budget: outputBudget}
}

// lineSize is at most what a window spends on s, which drops each \r.
func lineSize(s string) int { return len(s) + 1 - strings.Count(s, "\r") }

// footer fills as many of args as format has verbs, as awk's printf does,
// since some footers say where to resume and some do not.
func footer(format string, args ...int) string {
	n := strings.Count(format, "%d")
	vals := make([]any, 0, n)
	for _, a := range args[:min(n, len(args))] {
		vals = append(vals, a)
	}
	return fmt.Sprintf(format, vals...) + "\n"
}

// cutRunes shortens s to at most n bytes without splitting a rune.
func cutRunes(s string, n int) string {
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
