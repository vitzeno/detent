// Package host runs commands directly on the host via sh -c and
// captures bounded output.
package host

import (
	"bytes"
	"context"
	"errors"
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

// waitDelay is how long output is still read after sh exits.
const waitDelay = time.Second

// Shell runs commands directly on the host, unsandboxed. It satisfies
// engine.Runner structurally.
type Shell struct {
	limit int
}

// Run executes command via sh -c, sending each output line on events as
// it arrives. events may be nil, and Run closes it once output ends.
func (s *Shell) Run(ctx context.Context, command string, events chan<- StreamEvent) (Result, error) {
	if strings.TrimSpace(command) == "" {
		return Result{}, fmt.Errorf("host: empty command")
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}

	// Writers, not StdoutPipe: Wait then waits for exec's own copy, where
	// StdoutPipe's reader could lose output Wait closed under it.
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdout, cmd.Stderr = outW, errW
	// A backgrounded child keeps the pipes open, so stop reading soon after sh exits.
	cmd.WaitDelay = waitDelay

	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("host: %w", err)
	}

	var stdout, stderr bytes.Buffer
	var wg sync.WaitGroup
	scan := func(pipe io.Reader, isStderr bool, buf *bytes.Buffer) {
		defer wg.Done()
		capture.ScanCapped(pipe, isStderr, buf, s.limit, events)
		_, _ = io.Copy(io.Discard, pipe)
	}
	wg.Add(2)
	go scan(outR, false, &stdout)
	go scan(errR, true, &stderr)

	waitErr := cmd.Wait()
	_ = outW.Close()
	_ = errW.Close()
	wg.Wait()
	if events != nil {
		close(events)
	}
	if errors.Is(waitErr, exec.ErrWaitDelay) {
		waitErr = nil
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
