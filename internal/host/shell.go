// Package host runs commands directly on the host via sh -c and
// captures bounded output.
package host

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/vitzeno/detent/internal/capture"
)

// DefaultTimeout bounds a single command.
const DefaultTimeout = 30 * time.Second

// Shell runs commands directly on the host, unsandboxed.
// Satisfies agent.Runner structurally; callers wire it explicitly.
type Shell struct {
	limit int
}

// Run executes command via sh -c, sending each output line on events as
// it arrives. events may be nil; Run closes it once output ends.
func (s *Shell) Run(ctx context.Context, command string, events chan<- StreamEvent) (Result, error) {
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
		capture.ScanCapped(pipe, isStderr, buf, s.limit, events)
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
		Truncated: stdout.Len() >= s.limit || stderr.Len() >= s.limit,
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
