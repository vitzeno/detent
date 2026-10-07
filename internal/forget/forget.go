// Package forget removes what a deleted session left: its events, and
// the container nothing will ever resume. The log stays.
package forget

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// ErrLive is what a container remover wraps when another detent still
// holds the session, which stops the delete before the events go.
var ErrLive = errors.New("open in another detent")

// containerTimeout bounds removing one container, which talks to containerd.
const containerTimeout = 30 * time.Second

// Sessions is the store, declared at its consumer so the one path
// that destroys things tests without a database.
type Sessions interface {
	Delete(session uuid.UUID) (bool, error)
}

// Option is what a caller may add: a session always has events, and may or may
// not have a container or a lock.
type Option func(*watcher)

// WithContainers says how a deleted session's container goes. Without
// it there is nothing to remove, which is the no-sandbox case.
func WithContainers(remove func(ctx context.Context, sessionID string) error) Option {
	return func(w *watcher) { w.containers = remove }
}

// WithLocks holds a session for as long as its delete runs, failing with ErrLive
// when another detent holds it, which with no sandbox nothing else can tell.
func WithLocks(lock func(session uuid.UUID) (unlock func(), err error)) Option {
	return func(w *watcher) { w.lock = lock }
}

// Watch answers DeleteSession, refusing current, the running session. The
// stop waits for a delete in flight, and cancelling ctx abandons one.
func Watch(ctx context.Context, bus *event.Bus, sessions Sessions, current uuid.UUID, opts ...Option) func() {
	w := watcher{ctx: ctx, sessions: sessions, current: current}
	for _, o := range opts {
		o(&w)
	}
	return bus.Handle(event.Only(event.DeleteSessionKind, event.SessionStartedKind), func(rec event.Record) {
		switch v := rec.Event.(type) {
		case event.DeleteSession:
			w.forget(bus, v.Session)
		case event.SessionStarted:
			// After /new the running session is the new one, and the old can go.
			w.current = v.Session
		}
	})
}

type watcher struct {
	ctx        context.Context
	sessions   Sessions
	current    uuid.UUID
	containers func(ctx context.Context, sessionID string) error
	lock       func(session uuid.UUID) (unlock func(), err error)
}

func (w watcher) forget(bus *event.Bus, id uuid.UUID) {
	switch {
	case id == w.current:
		fail(bus, "this is the running session, so it cannot be deleted")
		return
	case w.sessions == nil:
		fail(bus, "nothing is recording, so there is nothing to delete")
		return
	}

	if w.lock != nil {
		unlock, err := w.lock(id)
		if err != nil {
			fail(bus, "session "+id.String()+" is "+heldBy(err)+", so it was not deleted")
			return
		}
		defer unlock()
	}

	// The container first: it can also tell that another detent has
	// this session open, and by then its events must still be there.
	stayed := w.container(id)
	switch {
	case errors.Is(stayed, ErrLive):
		fail(bus, "session "+id.String()+" is open in another detent, so it was not deleted")
		return
	case w.ctx.Err() != nil:
		// Quitting part way leaves the session whole rather than half gone.
		fail(bus, "detent is closing, so session "+id.String()+" was not deleted")
		return
	}

	gone, err := w.sessions.Delete(id)
	switch {
	case err != nil:
		fail(bus, err.Error())
		return
	case !gone:
		fail(bus, "no session "+id.String())
		return
	}

	// A container left behind is a leak rather than a lie. -prune has it.
	if stayed != nil {
		fail(bus, "deleted, but its container stayed: "+stayed.Error())
	} else {
		bus.Publish(event.Notice{Level: "info", Text: "deleted " + id.String()})
	}
	bus.Publish(event.ListSessions{})
}

func (w watcher) container(id uuid.UUID) error {
	if w.containers == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(w.ctx, containerTimeout)
	defer cancel()
	return w.containers(ctx, id.String())
}

func heldBy(err error) string {
	if errors.Is(err, ErrLive) {
		return "open in another detent"
	}
	return "not lockable (" + err.Error() + ")"
}

func fail(bus *event.Bus, text string) {
	bus.Publish(event.Notice{Level: "error", Text: text})
}
