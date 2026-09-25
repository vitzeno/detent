package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/engine"
	mcppkg "github.com/vitzeno/detent/internal/mcp"
	"github.com/vitzeno/detent/internal/sandbox"
	"github.com/vitzeno/detent/internal/store"
)

// Every wait here is bounded. Quitting is the one thing a human cannot
// take back by pressing another key, so no step may hang the terminal.
const (
	// engineGrace outlasts engine.DefaultStopGrace, so a Turn that will
	// not stop is the engine's own bound expiring rather than this one.
	engineGrace    = engine.DefaultStopGrace + time.Second
	drainGrace     = 2 * time.Second
	serverGrace    = 5 * time.Second
	containerGrace = 15 * time.Second
)

// shutdown closes what a session opened. Its fields are filled in as
// run() opens each thing, so an early return closes what was reached
// and nothing it never got to.
type shutdown struct {
	session uuid.UUID
	stop    context.CancelFunc
	engine  <-chan struct{}
	// grace bounds the wait on engine; zero means engineGrace.
	grace   time.Duration
	bus     *event.Bus
	unwatch []func()
	events  *store.Store
	// connect is the background dial, waited for before servers close
	connect   <-chan struct{}
	servers   *mcppkg.Invokers
	container *sandbox.Container
}

// close stops everything in the one order that keeps the session's
// last facts, then says how it went.
func (s shutdown) close() {
	errs := s.stopEngine()
	// Drained before the subscribers go, so what the engine just
	// published reaches the log and the store rather than dying here.
	if s.bus != nil {
		s.bus.Drain(drainGrace)
	}
	for _, f := range s.unwatch {
		f()
	}
	if s.events != nil {
		errs = appendErr(errs, s.events.Close())
	}
	if s.connect != nil {
		errs = appendErr(errs, wait(s.connect, serverGrace, "mcp servers finished connecting"))
	}
	if s.servers != nil {
		errs = appendErr(errs, bounded(serverGrace, "mcp servers", s.servers.Close))
	}
	if s.container != nil {
		ctx, cancel := context.WithTimeout(context.Background(), containerGrace)
		errs = appendErr(errs, s.container.Close(ctx))
		cancel()
	}
	s.report(os.Stderr, errs)
}

// stopEngine cancels the loop and waits for the facts it still owes,
// bounded so a wedged engine cannot hold the terminal either.
func (s shutdown) stopEngine() []error {
	if s.stop != nil {
		s.stop()
	}
	if s.engine == nil {
		return nil
	}
	grace := s.grace
	if grace == 0 {
		grace = engineGrace
	}
	select {
	case <-s.engine:
		return nil
	case <-time.After(grace):
		return []error{fmt.Errorf("the running request did not stop in %s; its last steps may be missing", grace)}
	}
}

// report is what a human reads once the screen is back: what failed, if
// anything, and the id that picks this session up again.
func (s shutdown) report(w io.Writer, errs []error) {
	for _, err := range errs {
		fmt.Fprintln(w, "detent:", err)
	}
	// A bus means a session ran. -sessions, -prune and -mcp have
	// nothing to resume and nothing to say.
	if s.bus == nil {
		return
	}
	if s.events == nil {
		fmt.Fprintln(w, "detent: session was not recorded, so there is nothing to resume")
		return
	}
	fmt.Fprintf(w, "detent: session saved — detent -resume %s\n", s.session)
}

// bounded puts a deadline on a close that has none. Invokers.Close
// walks its servers serially and the SDK allows each ten seconds to
// die, so three sulking servers would hold the terminal for thirty.
func bounded(d time.Duration, what string, fn func() error) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		// The goroutine outlives this, which costs nothing: the only
		// thing left to do is exit.
		return fmt.Errorf("%s did not close in %s", what, d)
	}
}

// wait blocks on a background goroutine, bounded. Its ctx is already
// cancelled by then, so this is how long unwinding takes, not the work.
func wait(done <-chan struct{}, d time.Duration, what string) error {
	select {
	case <-done:
		return nil
	case <-time.After(d):
		return fmt.Errorf("gave up after %s waiting until %s", d, what)
	}
}

func appendErr(errs []error, err error) []error {
	if err == nil {
		return errs
	}
	return append(errs, err)
}
