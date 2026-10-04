// Package capture holds the bounded-output primitives every command
// backend shares: a capped Result, a StreamEvent for live output, and
// the scanner that produces both.
package capture

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"unicode/utf8"
)

const (
	// MaxOutputBytes caps each captured stream.
	MaxOutputBytes = 8 * 1024
	// MaxResultBytes bounds one result in the transcript. Above every tool's own
	// window, so the line saying how much is left is never what gets cut.
	MaxResultBytes = 3 * MaxOutputBytes
)

const (
	// readSize is the reader's buffer: a longer line arrives in pieces.
	readSize = 64 * 1024
	// maxLineBytes is the most of one line a StreamEvent carries.
	maxLineBytes = 1024 * 1024
)

// Result is a command's captured outcome. ExitCode is 0 for a command
// that was stopped, and Truncated covers either stream.
type Result struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Truncated bool
}

// StreamEvent is one line of live output from a running command.
type StreamEvent struct {
	Stderr bool
	Line   string
}

// ScanCapped reads r by line, capping buf at limit bytes and sending each line on
// events if non-nil, which the caller must drain. An overlong line is cut, not the scan.
func ScanCapped(r io.Reader, isStderr bool, buf *bytes.Buffer, limit int, events chan<- StreamEvent) (truncated bool, err error) {
	lw := &limitedWriter{buf: buf, limit: limit}
	br := bufio.NewReaderSize(r, readSize)
	var line []byte
	emit := func() {
		line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
		if lw.full {
			lw.dropped = true
		} else {
			_, _ = lw.Write(append(line, '\n'))
		}
		if events != nil {
			events <- StreamEvent{Stderr: isStderr, Line: string(line)}
		}
		line = line[:0]
	}
	for {
		chunk, rerr := br.ReadSlice('\n')
		// A line cut to fit a StreamEvent marks the result truncated too.
		if room := maxLineBytes - len(line); len(chunk) > room {
			chunk, lw.dropped = chunk[:room], true
		}
		line = append(line, chunk...)
		switch {
		case rerr == nil:
			emit()
		case errors.Is(rerr, bufio.ErrBufferFull):
		default:
			if len(line) > 0 {
				emit()
			}
			if errors.Is(rerr, io.EOF) {
				rerr = nil
			}
			return lw.dropped, rerr
		}
	}
}

// Clip bounds s to about n bytes, keeping its head and tail, since a
// failure is as often at the end of output as at the start.
func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	head, tail := runeFloor(s, n/4), runeCeil(s, len(s)-n*3/4)
	return s[:head] + "\n…[truncated]…\n" + s[tail:]
}

// limitedWriter keeps the first limit bytes and drops the rest, cutting
// only at a rune boundary.
type limitedWriter struct {
	buf     *bytes.Buffer
	limit   int
	full    bool
	dropped bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buf.Len()
	if w.full || remaining <= 0 {
		w.full, w.dropped = true, w.dropped || len(p) > 0
		return len(p), nil
	}
	if len(p) <= remaining {
		return w.buf.Write(p)
	}
	n := remaining
	for n > 0 && !utf8.RuneStart(p[n]) {
		n--
	}
	w.buf.Write(p[:n])
	w.full, w.dropped = true, true
	return len(p), nil
}

// runeFloor moves i back to the start of the rune it is in.
func runeFloor(s string, i int) int {
	for i > 0 && i < len(s) && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}

// runeCeil moves i on to the start of the next rune.
func runeCeil(s string, i int) int {
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return i
}
