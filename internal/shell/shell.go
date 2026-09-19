// Package shell runs commands via sh -c and captures bounded output.
package shell

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
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

// Run executes command via sh -c; a non-zero exit is a Result, not an error.
func Run(ctx context.Context, command string) (Result, error) {
	if strings.TrimSpace(command) == "" {
		return Result{}, fmt.Errorf("shell: empty command")
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{buf: &stdout, limit: MaxOutputBytes}
	cmd.Stderr = &limitedWriter{buf: &stderr, limit: MaxOutputBytes}

	err := cmd.Run()
	res := Result{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Truncated: stdout.Len() >= MaxOutputBytes || stderr.Len() >= MaxOutputBytes,
	}

	// Context expiry is an error: CommandContext kills the process, which would otherwise look like a non-zero exit.
	if ctx.Err() != nil {
		return res, fmt.Errorf("shell: %w", ctx.Err())
	}
	if err == nil {
		res.ExitCode = 0
		return res, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	return res, fmt.Errorf("shell: %w", err)
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
