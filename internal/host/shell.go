// Package host runs commands directly on the host, through sh, PowerShell 7
// or Git Bash, and captures bounded output.
package host

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vitzeno/detent/internal/capture"
)

const (
	// DefaultTimeout bounds a command whose caller set no deadline. The
	// engine and usercommand always set one.
	DefaultTimeout = 30 * time.Second
	// waitDelay is how long output is still read after the shell exits.
	waitDelay = time.Second
)

// ErrEmptyCommand is a command with nothing to run.
var ErrEmptyCommand = errors.New("host: empty command")

// secrets are detent's own credentials, kept from every command it runs. A
// denylist, so another token, such as one loaded from .env, still passes.
var secrets = []string{"DETENT_API_KEY", "OPENROUTER_API_KEY", "OPENAI_API_KEY", "TYPESAFE_API_KEY"}

// Shell runs commands directly on the host, unsandboxed. It satisfies
// engine.Runner structurally.
type Shell struct {
	limit int
	// dialect is Sh when empty, and path is the program that runs it.
	dialect string
	path    string
}

// Option configures a Shell.
type Option func(*Shell)

// NewShell builds a Shell, capping output at capture.MaxOutputBytes unless overridden.
func NewShell(opts ...Option) *Shell {
	s := &Shell{limit: capture.MaxOutputBytes}
	for _, o := range opts {
		o(s)
	}
	return s
}

// WithLimit caps the bytes captured from each of stdout and stderr.
func WithLimit(limit int) Option {
	return func(s *Shell) {
		s.limit = limit
	}
}

// Dialect is the shell commands are written for.
func (s *Shell) Dialect() string { return cmp.Or(s.dialect, Sh) }

// Run executes command, sending each output line on events, which may be nil.
// It sends nothing after it returns, and the caller closes events.
func (s *Shell) Run(ctx context.Context, command string, events chan<- capture.StreamEvent) (capture.Result, error) {
	if strings.TrimSpace(command) == "" {
		return capture.Result{}, ErrEmptyCommand
	}
	limit := s.limit
	if limit <= 0 {
		limit = capture.MaxOutputBytes
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}

	argv, err := s.argv(command)
	if err != nil {
		return capture.Result{}, err
	}

	// Writers, not StdoutPipe: Wait then waits for exec's own copy, where
	// StdoutPipe's reader could lose output Wait closed under it.
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = outW, errW
	cmd.Env = environ(os.Environ())
	// A backgrounded child keeps the pipes open, so stop reading soon after the shell exits.
	cmd.WaitDelay = waitDelay
	g := newGroup(cmd)

	if err := cmd.Start(); err != nil {
		return capture.Result{}, fmt.Errorf("host: %w", err)
	}
	g.started(cmd)

	var stdout, stderr bytes.Buffer
	var outCut, errCut bool
	var wg sync.WaitGroup
	scan := func(pipe io.Reader, isStderr bool, buf *bytes.Buffer, cut *bool) {
		defer wg.Done()
		*cut, _ = capture.ScanCapped(pipe, isStderr, buf, limit, events)
		_, _ = io.Copy(io.Discard, pipe)
	}
	wg.Add(2)
	go scan(outR, false, &stdout, &outCut)
	go scan(errR, true, &stderr, &errCut)

	waitErr := cmd.Wait()
	g.done()
	_ = outW.Close()
	_ = errW.Close()
	wg.Wait()
	if errors.Is(waitErr, exec.ErrWaitDelay) {
		waitErr = nil
	}

	res := capture.Result{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Truncated: outCut || errCut,
	}
	if waitErr == nil {
		return res, nil
	}
	if ctx.Err() != nil {
		return res, fmt.Errorf("host: %w", ctx.Err())
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	return res, fmt.Errorf("host: %w", waitErr)
}

// environ is the parent's environment without detent's own secrets, so
// printenv cannot put an API key in the transcript or the store.
func environ(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(secrets, name) {
			out = append(out, kv)
		}
	}
	return out
}
