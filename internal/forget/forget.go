// Package forget removes what a deleted session left: its events, and
// the container nothing will ever resume. The log stays
package forget

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// Watch answers DeleteSession and reports the outcome either way.
// current is the running session, the one thing that cannot go.
func Watch(bus *event.Bus, sessions Sessions, current uuid.UUID, opts ...Option) func() {
	w := watcher{sessions: sessions, current: current}
	for _, o := range opts {
		o(&w)
	}
	intents, stop := bus.Subscribe(event.Only(event.DeleteSessionKind))
	go func() {
		for rec := range intents {
			w.forget(bus, rec.Event.(event.DeleteSession).Session)
		}
	}()
	return stop
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

	gone, err := w.sessions.Delete(id)
	switch {
	case err != nil:
		fail(bus, err.Error())
		return
	case !gone:
		fail(bus, "no session "+id.String())
		return
	}

	// Best effort past here: the session is already gone, and a
	// container left behind is a leak rather than a lie. -prune has it.
	if err := w.container(id); err != nil {
		fail(bus, "deleted, but its container stayed: "+err.Error())
	} else {
		bus.Publish(event.Notice{Level: "info", Text: "deleted " + id.String()})
	}
	bus.Publish(event.ListSessions{})
}

func (w watcher) container(id uuid.UUID) error {
	if w.containers == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return w.containers(ctx, id.String())
}

func fail(bus *event.Bus, text string) {
	bus.Publish(event.Notice{Level: "error", Text: text})
}
