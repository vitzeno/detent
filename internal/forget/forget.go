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
// Wired beside the other watchers.
func Watch(bus *event.Bus, o Options) func() {
	intents, stop := bus.Subscribe(event.Only(event.DeleteSessionKind))
	go func() {
		for rec := range intents {
			o.forget(bus, rec.Event.(event.DeleteSession).Session)
		}
	}()
	return stop
}

// Options is what forgetting needs. Sessions and Containers are taken
// rather than imported, so this needs neither a store nor a daemon to
// test — which matters for the one path that destroys things.
type Options struct {
	Sessions Sessions
	// Current is the running session, the one thing that cannot go.
	Current uuid.UUID
	// Containers removes a session's container. Nil when this run has
	// no sandbox, and so nothing to remove.
	Containers func(ctx context.Context, sessionID string) error
}

// Sessions is the store, declared here at its consumer.
type Sessions interface {
	Delete(session uuid.UUID) (bool, error)
}

func (o Options) forget(bus *event.Bus, id uuid.UUID) {
	switch {
	case id == o.Current:
		fail(bus, "this is the running session, so it cannot be deleted")
		return
	case o.Sessions == nil:
		fail(bus, "nothing is recording, so there is nothing to delete")
		return
	}

	gone, err := o.Sessions.Delete(id)
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
	if err := o.container(id); err != nil {
		fail(bus, "deleted, but its container stayed: "+err.Error())
	} else {
		bus.Publish(event.Notice{Level: "info", Text: "deleted " + id.String()})
	}
	bus.Publish(event.ListSessions{})
}

func (o Options) container(id uuid.UUID) error {
	if o.Containers == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return o.Containers(ctx, id.String())
}

func fail(bus *event.Bus, text string) {
	bus.Publish(event.Notice{Level: "error", Text: text})
}
