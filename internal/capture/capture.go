// Package capture holds the bounded-output primitives every command
// backend shares: a capped Result, a StreamEvent for live output, and
// the scanner that produces both.
package capture

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// MaxOutputBytes caps each captured stream.
const MaxOutputBytes = 8 * 1024

// StreamEvent is one line of live output from a running command.
type StreamEvent struct {
	Stderr bool
	Line   string
}

// Result is a command's captured outcome.
type Result struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Truncated bool
}

// Summary renders a one-line result.
func (r Result) Summary() string {
	lines := 0
	for _, s := range []string{r.Stdout, r.Stderr} {
		if s == "" {
			continue
		}
		lines += strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
	}
	s := fmt.Sprintf("exit %d, %d lines", r.ExitCode, lines)
	if r.Truncated {
		s += " (truncated)"
	}
	return s
}

// ScanCapped reads r line by line until EOF, capping buf at limit
// bytes and emitting a StreamEvent per line if events is non-nil.
func ScanCapped(r io.Reader, isStderr bool, buf *bytes.Buffer, limit int, events chan<- StreamEvent) {
	lw := &limitedWriter{buf: buf, limit: limit}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		_, _ = lw.Write([]byte(line + "\n"))
		if events != nil {
			events <- StreamEvent{Stderr: isStderr, Line: line}
		}
	}
}

type limitedWriter struct {
	buf   *bytes.Buffer
	limit int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buf.Len()
	if remaining <= 0 {
		return len(p), nil // discard, but report success so exec continues
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	return w.buf.Write(p)
}
