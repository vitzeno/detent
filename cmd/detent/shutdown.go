package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/engine"
	mcppkg "github.com/vitzeno/detent/internal/mcp"
	"github.com/vitzeno/detent/internal/sandbox"
	"github.com/vitzeno/detent/internal/store"
	"github.com/vitzeno/detent/internal/worktree"
)

// Every wait here is bounded. Quitting is the one thing a human cannot
// take back by pressing another key, so no step may hang the terminal.
const (
	// userCommandGrace bounds unwinding a cancelled command, not running one.
	userCommandGrace = 3 * time.Second
	// engineGrace outlasts engine.DefaultStopGrace, so a Turn that will
	// not stop is the engine's own bound expiring rather than this one.
	engineGrace    = engine.DefaultStopGrace + time.Second
	drainGrace     = 2 * time.Second
	serverGrace    = 5 * time.Second
	containerGrace = 15 * time.Second
)

// shutdown closes what a session opened. run() fills its fields as it
// opens each thing, so an early return closes only what was reached.
type shutdown struct {
	// session is the one to resume, moved by /new while the TUI runs.
	session *latestSession
	// userCommand stops the human's own command. Not in unwatch: that list
	// runs after the drain, and a fact published then reaches nobody.
	userCommand func()
	// review reads the worktree, so it stops before that closes.
	review func()
	stop   context.CancelFunc
	engine <-chan struct{}
	// grace bounds the wait on engine, zero meaning engineGrace.
	grace   time.Duration
	bus     *event.Bus
	unwatch []func()
	events  *store.Store
	// connect is the background dial, waited for before servers close.
	connect   <-chan struct{}
	servers   *mcppkg.Invokers
	container *sandbox.Container
	worktree  *worktree.Dir
}

// latestSession is the session SessionStarted last named. Set from the bus,
// read at shutdown, so it is guarded.
type latestSession struct {
	mu sync.Mutex
	id uuid.UUID
}

func (l *latestSession) set(id uuid.UUID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.id = id
}

func (l *latestSession) get() uuid.UUID {
	if l == nil {
		return uuid.Nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.id
}

// close stops everything in the one order that keeps the session's
// last facts, then says how it went.
func (s shutdown) close() {
	// Before the engine, so the message the command owes the
	// transcript still has somewhere to land.
	errs := s.stopUserCommand()
	errs = append(errs, s.stopEngine()...)
	// After the engine and review, the two things that checkpoint.
	if s.review != nil {
		errs = appendErr(errs, bounded(drainGrace, "review", func() error { s.review(); return nil }))
	}
	if s.worktree != nil {
		errs = appendErr(errs, s.worktree.Close())
	}
	// Drained before the subscribers go, so what the engine just
	// published reaches the log and the store rather than dying here.
	if s.bus != nil {
		s.bus.Drain(drainGrace)
	}
	for _, f := range s.unwatch {
		errs = appendErr(errs, bounded(drainGrace, "a subscriber", func() error { f(); return nil }))
	}
	// Read before the store closes, so quitting can say how to resume by name.
	name := s.name()
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
	s.report(os.Stderr, errs, name)
}

// stopUserCommand cancels the human's command and waits for its last facts,
// then lets them reach the engine before that stops too.
func (s shutdown) stopUserCommand() []error {
	if s.userCommand == nil {
		return nil
	}
	err := bounded(userCommandGrace, "the running command", func() error { s.userCommand(); return nil })
	if s.bus != nil {
		s.bus.Settle(userCommandGrace)
	}
	return appendErr(nil, err)
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

// name is what the human called the session, empty when unnamed or unread.
func (s shutdown) name() string {
	if s.events == nil {
		return ""
	}
	name, _ := s.events.Name(s.session.get())
	return name
}

// report is what a human reads once the screen is back: what failed, if
// anything, and the name or id that picks this session up again.
func (s shutdown) report(w io.Writer, errs []error, name string) {
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
	if name != "" {
		fmt.Fprintf(w, "detent: session %q saved, resume with: detent -resume %s\n", name, shellWord(name))
		return
	}
	fmt.Fprintf(w, "detent: session saved, resume with: detent -resume %s\n", s.session.get())
}

// bounded puts a deadline on a close that has none. Invokers.Close is
// serial and the SDK gives each server ten seconds to die.
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

// shellWord is s as one word a shell reads back unchanged, quoted only when it must be.
func shellWord(s string) string {
	plain := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.:@/+=,", r)
	}
	if !strings.ContainsFunc(s, func(r rune) bool { return !plain(r) }) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
