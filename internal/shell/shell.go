// Package shell runs commands via sh -c and captures bounded output.
package shell

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// DefaultTimeout bounds a single command.
const DefaultTimeout = 30 * time.Second

// MaxOutputBytes caps each captured stream.
const MaxOutputBytes = 8 * 1024

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
