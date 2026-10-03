// Package humanshell runs the commands a human types. A Shell is a
// fifth scope beside Session, Turn, Step and Call: no model asked for
// one, so nothing here assesses, approves or judges it.
package humanshell

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
)

const (
	// shellMax is a backstop for a human who walked away, not a bound
	// on their work: esc stops it and they are the one watching.
	shellMax       = 30 * time.Minute
	stoppedByHuman = "stopped by the human"
)

// Watch runs what RunCommand carries and stops it on CancelCommand.
// where is "host" or "sandbox".
func Watch(bus *event.Bus, runner Runner, where string) func() {
	s := &shell{bus: bus, runner: runner, where: where}
	intents, unsub := bus.Subscribe(event.Only(event.RunCommandKind, event.CancelCommandKind))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for rec := range intents {
			switch v := rec.Event.(type) {
			case event.RunCommand:
				s.start(v.Text)
			case event.CancelCommand:
				s.cancel(v.Shell)
			}
		}
	}()
	// Waits for the last facts, so a caller can order this before the drain.
	return func() {
		unsub()
		<-done
		s.cancel(uuid.Nil)
		s.wg.Wait()
	}
}

// Runner executes one command. Declared here rather than imported, so
// this package depends on event and capture alone. It sends nothing on
// events after it returns, and the caller closes events.
type Runner interface {
	Run(ctx context.Context, command string, events chan<- capture.StreamEvent) (capture.Result, error)
}

// shell holds the one command that may be in flight. One at a time:
// two interleaved outputs are unreadable whatever the runner allows.
type shell struct {
	bus    *event.Bus
	runner Runner
	where  string

	wg sync.WaitGroup
	mu sync.Mutex
	id uuid.UUID
	// stop is nil exactly when nothing is running.
	stop context.CancelFunc
}

// start refuses a second command rather than queueing it: the human
// can see the first one running.
func (s *shell) start(command string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}
	// Background, stopped by the func Watch returned. The deadline is
	// set here so host.Shell does not apply the model's 30s.
	ctx, cancel := context.WithTimeout(context.Background(), shellMax)
	id := uuid.Must(uuid.NewV7())

	s.mu.Lock()
	busy := s.stop != nil
	if !busy {
		s.id, s.stop = id, cancel
	}
	s.mu.Unlock()

	if busy {
		cancel()
		s.bus.Publish(event.Notice{Level: "warn", Text: "a command is already running"})
		return
	}
	s.wg.Go(func() {
		defer cancel()
		s.run(ctx, id, command)
		s.done(id)
	})
}

// run publishes the two facts the command is, then the message the
// model reads. The engine appends that at a Step boundary.
func (s *shell) run(ctx context.Context, id uuid.UUID, command string) {
	s.bus.Publish(event.ShellStarted{Shell: id, Command: command, Runner: s.where})

	lines := make(chan capture.StreamEvent, 64)
	relayed := make(chan struct{})
	go func() {
		defer close(relayed)
		for l := range lines {
			s.bus.Publish(event.OutputChunk{Call: id, Line: l.Line, Stderr: l.Stderr})
		}
	}()

	began := time.Now()
	res, err := runSafely(ctx, s.runner, command, lines)
	took := time.Since(began)
	close(lines)
	<-relayed

	out := event.Result{
		ExitCode: res.ExitCode, Stdout: res.Stdout, Stderr: res.Stderr, Truncated: res.Truncated,
	}
	if err != nil {
		out.Err = err.Error()
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		out.Err = stoppedByHuman
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		out.Err = fmt.Sprintf("gave up after %s", shellMax)
	}
	s.bus.Publish(event.ShellEnded{Shell: id, Result: out, Took: took})
	s.bus.Publish(event.NoteContext{Text: transcribe(command, s.where, out)})
}

// cancel stops the running command. A zero id means whichever one
// that is, which is what esc knows without tracking it.
func (s *shell) cancel(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop != nil && (id == uuid.Nil || id == s.id) {
		s.stop()
	}
}

func (s *shell) done(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.id == id {
		s.id, s.stop = uuid.Nil, nil
	}
}

// runSafely turns a panicking Runner into a failed command rather
// than a dead session.
func runSafely(ctx context.Context, r Runner, cmd string, lines chan<- capture.StreamEvent) (res capture.Result, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("runner panicked: %v", v)
		}
	}()
	return r.Run(ctx, cmd, lines)
}
