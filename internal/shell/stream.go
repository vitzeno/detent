package shell

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// StreamEvent is one line of live output from a running command.
type StreamEvent struct {
	Stderr bool
	Line   string
}

// Stream behaves like Run but delivers each output line to onEvent, which is called serially and must not block.
func Stream(ctx context.Context, command string, onEvent func(StreamEvent)) (Result, error) {
	if strings.TrimSpace(command) == "" {
		return Result{}, fmt.Errorf("shell: empty command")
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("shell: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return Result{}, fmt.Errorf("shell: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("shell: %w", err)
	}

	var stdout, stderr bytes.Buffer
	var wg sync.WaitGroup
	var evMu sync.Mutex
	emit := func(e StreamEvent) {
		if onEvent == nil {
			return
		}
		evMu.Lock()
		defer evMu.Unlock()
		onEvent(e)
	}
	scan := func(pipe io.Reader, isStderr bool, buf *bytes.Buffer) {
		defer wg.Done()
		lw := &limitedWriter{buf: buf, limit: MaxOutputBytes}
		sc := bufio.NewScanner(pipe)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			_, _ = lw.Write([]byte(line + "\n"))
			emit(StreamEvent{Stderr: isStderr, Line: line})
		}
	}
	wg.Add(2)
	go scan(stdoutPipe, false, &stdout)
	go scan(stderrPipe, true, &stderr)

	waitErr := cmd.Wait()
	wg.Wait()

	res := Result{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Truncated: stdout.Len() >= MaxOutputBytes || stderr.Len() >= MaxOutputBytes,
	}

	if ctx.Err() != nil {
		return res, fmt.Errorf("shell: %w", ctx.Err())
	}
	if waitErr == nil {
		res.ExitCode = 0
		return res, nil
	}
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	return res, fmt.Errorf("shell: %w", waitErr)
}
