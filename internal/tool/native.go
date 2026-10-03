package tool

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/vitzeno/detent/internal/capture"
)

// Native is a Tool that also runs in this process, which is how it runs on the
// host on every OS. The sandbox still runs Lower, and both must print the same.
type Native interface {
	Tool
	Run(ctx context.Context, args Args) capture.Result
}

// failed is a native tool refusing or failing, said the way a command would.
func failed(code int, format string, a ...any) capture.Result {
	return capture.Result{ExitCode: code, Stderr: fmt.Sprintf(format, a...) + "\n"}
}

// stopped is a native tool cut short by its context, keeping what it printed.
func stopped(name, out string, err error) capture.Result {
	return capture.Result{ExitCode: 1, Stdout: out, Stderr: name + ": stopped: " + err.Error() + "\n"}
}

const (
	// readChunk is how much of a line is read at a time.
	readChunk = 64 * 1024
	// lineKeep is as much of one line as a window can need: the budget,
	// a \r and a byte to tell that it is over.
	lineKeep = outputBudget + 2
	// maxEditBytes is the largest file write_file and edit_file read whole.
	maxEditBytes = 50 << 20
)

// windower is window's awk script in Go, fed a line at a time.
type windower struct {
	b             strings.Builder
	start, limit  int
	n, used, stop int
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
	case w.used > 0 && (long || w.used+len(line)+1 > outputBudget):
		w.stop = w.n
		return
	}
	if long || len(line) > outputBudget {
		line = cutRunes(line, outputBudget) + " [line cut]"
	}
	w.b.WriteString(line)
	w.b.WriteByte('\n')
	w.used += len(line) + 1
}

func (w *windower) end(more, empty string) string {
	switch {
	case w.stop > 0:
		w.b.WriteString(footer(more, w.n-w.stop+1, w.stop))
	case w.n < w.start:
		w.b.WriteString(footer(empty, w.n))
	}
	return w.b.String()
}

// windowLines is window's awk script in Go, holding at most lineKeep bytes
// of any line however long, and stopping when ctx does.
func windowLines(ctx context.Context, r io.Reader, start, limit int, more, empty string) (string, error) {
	w := windower{start: start, limit: limit}
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

// openRegular opens p to read, refusing anything but a regular file before
// opening it, since opening a FIFO blocks until something writes to it.
func openRegular(p string) (*os.File, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if err := regular(p, info); err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	if info, err = f.Stat(); err == nil {
		err = regular(p, info)
	}
	if err != nil {
		_ = f.Close() // read only, so closing cannot lose anything
		return nil, err
	}
	return f, nil
}

// regular refuses a directory as a read of one fails, and a FIFO, socket or
// device outright, since reading one can block or never end.
func regular(p string, info fs.FileInfo) error {
	m := info.Mode()
	switch {
	case m.IsRegular():
		return nil
	case m.IsDir():
		return &fs.PathError{Op: "read", Path: p, Err: syscall.EISDIR}
	}
	kind := "a device"
	switch {
	case m&fs.ModeNamedPipe != 0:
		kind = "a named pipe"
	case m&fs.ModeSocket != 0:
		kind = "a socket"
	}
	return &fs.PathError{Op: "open", Path: p, Err: fmt.Errorf("is %s, not a regular file, so read it with a command if you must", kind)}
}

// readCapped reads a regular file whole, refusing one over limit bytes.
func readCapped(ctx context.Context, p string, limit int64) ([]byte, error) {
	f, err := openRegular(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read only, so closing cannot lose anything
	if info, err := f.Stat(); err == nil && info.Size() > limit {
		return nil, tooLarge(p, limit)
	}
	b, err := io.ReadAll(io.LimitReader(ctxReader{ctx, f}, limit+1))
	if err == nil && int64(len(b)) > limit {
		return nil, tooLarge(p, limit) // it grew since
	}
	return b, err
}

func tooLarge(p string, limit int64) error {
	return &fs.PathError{Op: "read", Path: p, Err: fmt.Errorf("larger than %d MB, so change it with a command", limit>>20)}
}

// ctxReader stops reading once ctx is done.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
