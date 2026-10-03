// Package usercommand runs the commands a human types. A user command is a
// fifth scope beside Session, Turn, Step and tool call: no model asked for
// one, so nothing here assesses, approves or judges it.
package usercommand

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
	// commandMax is a backstop for a human who walked away, not a bound
	// on their work: esc stops it and they are the one watching.
	commandMax     = 30 * time.Minute
	stoppedByHuman = "stopped by the human"
)

// Runner executes one command. Declared here rather than imported, so
// this package depends on event and capture alone. It sends nothing on
// events after it returns, and the caller closes events.
type Runner interface {
	Run(ctx context.Context, command string, events chan<- capture.StreamEvent) (capture.Result, error)
}

// Watch runs what RunCommand carries and stops it on CancelCommand.
// where is "host" or "sandbox". Cancelling ctx stops a running command.
func Watch(ctx context.Context, bus *event.Bus, runner Runner, where string) func() {
	s := &commands{ctx: ctx, bus: bus, runner: runner, where: where}
	unhandle := bus.Handle(event.Only(event.RunCommandKind, event.CancelCommandKind), func(rec event.Record) {
		switch v := rec.Event.(type) {
		case event.RunCommand:
			s.start(v.Text)
		case event.CancelCommand:
			s.cancel(v.UserCommand)
		}
	})
	// Waits for the last facts, so a caller can order this before the drain.
	return func() {
		unhandle()
		s.cancel(uuid.Nil)
		s.wg.Wait()
	}
}

// commands holds the one command that may be in flight. One at a time:
// two interleaved outputs are unreadable whatever the runner allows.
type commands struct {
	ctx    context.Context
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
func (s *commands) start(command string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}
	// Stopped by the func Watch returned too. The deadline is set here
	// so host.Shell does not apply the model's own.
	ctx, cancel := context.WithTimeout(s.ctx, commandMax)
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
func (s *commands) run(ctx context.Context, id uuid.UUID, command string) {
	s.bus.Publish(event.UserCommandStarted{UserCommand: id, Command: command, Runner: s.where})

	lines := make(chan capture.StreamEvent, 64)
	relayed := make(chan struct{})
	go func() {
		defer close(relayed)
		for l := range lines {
			s.bus.Publish(event.OutputChunk{UserCommand: id, Line: l.Line, Stderr: l.Stderr})
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
		out.Err = fmt.Sprintf("gave up after %s", commandMax)
	}
	s.bus.Publish(event.UserCommandEnded{UserCommand: id, Result: out, Took: took})
	s.bus.Publish(event.NoteContext{Text: transcribe(command, s.where, out)})
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

func (s *commands) done(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.id == id {
		s.id, s.stop = uuid.Nil, nil
	}
}

// cancel stops the running command. A zero id means whichever one
// that is, which is what esc knows without tracking it.
func (s *commands) cancel(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop != nil && (id == uuid.Nil || id == s.id) {
		s.stop()
	}
}
