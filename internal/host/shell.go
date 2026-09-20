// Package host runs commands directly on the host via sh -c and
// captures bounded output.
package host

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
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

// StreamEvent is one line of live output from a running command.
type StreamEvent struct {
	Stderr bool
	Line   string
}

// Shell runs commands directly on the host, unsandboxed.
// Satisfies agent.Runner structurally; callers wire it explicitly.
type Shell struct{}

// Run executes command via sh -c, sending each output line on events as
// it arrives. events may be nil; Run closes it once output ends.
func (Shell) Run(ctx context.Context, command string, events chan<- StreamEvent) (Result, error) {
	if strings.TrimSpace(command) == "" {
		return Result{}, fmt.Errorf("host: empty command")
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("host: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return Result{}, fmt.Errorf("host: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("host: %w", err)
	}

	var stdout, stderr bytes.Buffer
	var wg sync.WaitGroup
	scan := func(pipe io.Reader, isStderr bool, buf *bytes.Buffer) {
		defer wg.Done()
		lw := &limitedWriter{buf: buf, limit: MaxOutputBytes}
		sc := bufio.NewScanner(pipe)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			_, _ = lw.Write([]byte(line + "\n"))
			if events != nil {
				events <- StreamEvent{Stderr: isStderr, Line: line}
			}
		}
	}
	wg.Add(2)
	go scan(stdoutPipe, false, &stdout)
	go scan(stderrPipe, true, &stderr)

	waitErr := cmd.Wait()
	wg.Wait()
	if events != nil {
		close(events)
	}

	res := Result{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Truncated: stdout.Len() >= MaxOutputBytes || stderr.Len() >= MaxOutputBytes,
	}

	if ctx.Err() != nil {
		return res, fmt.Errorf("host: %w", ctx.Err())
	}
	if waitErr == nil {
		res.ExitCode = 0
		return res, nil
	}
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	return res, fmt.Errorf("host: %w", waitErr)
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
