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

// Watch answers DeleteSession and reports the outcome either way.
// current is the running session, the one thing that cannot go. The
// stop waits for a delete in flight, and cancelling ctx abandons one.
func Watch(ctx context.Context, bus *event.Bus, sessions Sessions, current uuid.UUID, opts ...Option) func() {
	w := watcher{ctx: ctx, sessions: sessions, current: current}
	for _, o := range opts {
		o(&w)
	}
	return bus.Handle(event.Only(event.DeleteSessionKind), func(rec event.Record) {
		if v, ok := rec.Event.(event.DeleteSession); ok {
			w.forget(bus, v.Session)
		}
	})
}

// Sessions is the store, declared at its consumer so the one path
// that destroys things tests without a database.
type Sessions interface {
	Delete(session uuid.UUID) (bool, error)
}

// Option is what a caller may add. Containers is the only one: a
// session always has events, and may or may not have a container.
type Option func(*watcher)

// WithContainers says how a deleted session's container goes. Without
// it there is nothing to remove, which is the no-sandbox case.
func WithContainers(remove func(ctx context.Context, sessionID string) error) Option {
	return func(w *watcher) { w.containers = remove }
}

type watcher struct {
	ctx        context.Context
	sessions   Sessions
	current    uuid.UUID
	containers func(ctx context.Context, sessionID string) error
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

	// The container first: only it can tell that another detent has
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

func fail(bus *event.Bus, text string) {
	bus.Publish(event.Notice{Level: "error", Text: text})
}
