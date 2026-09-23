package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/store"
)

// The gate: what the bus published is what the database holds.
func TestWatch_StoresEveryFactInOrder(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	bus := event.New()
	stop := store.Watch(bus, s, session)

	turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	published := []event.Event{
		event.SessionStarted{Session: session, Model: "m"},
		event.TurnStarted{Turn: turn, N: 1, Prompt: "go"},
		event.Appended{Turn: turn, Messages: []event.Message{{Role: event.RoleUser, Content: "go"}}},
		event.CallProposed{Call: call, Tool: "bash", Args: map[string]any{"command": "ls"}},
		event.CallEnded{Call: call, Result: event.Result{Stdout: "a.go\n"}},
		event.TurnEnded{Turn: turn, Reason: event.EndDone},
	}
	for _, e := range published {
		bus.Publish(e)
	}
	bus.Drain(3 * time.Second)
	stop()

	got, err := s.Replay(session)
	require.NoError(t, err)
	require.Len(t, got, len(published), "every fact must land")
	for i, want := range published {
		assert.Equal(t, want, got[i].Event, "record %d", i)
		assert.EqualValues(t, i+1, got[i].Ordinal, "and keep the bus's ordinal")
	}
}

// Live output is the one fact a replay has no use for: a replayed Call
// has already finished, so there is nothing to redraw.
func TestWatch_SkipsLiveOutputAndIntents(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	bus := event.New()
	stop := store.Watch(bus, s, session)

	bus.Publish(event.SessionStarted{Session: session, Model: "m"})
	call := uuid.Must(uuid.NewV7())
	for range 50 {
		bus.Publish(event.OutputChunk{Call: call, Line: "noise"})
	}
	bus.Publish(event.SubmitPrompt{Text: "an intent"})
	bus.Publish(event.Abort{Turn: uuid.Must(uuid.NewV7())})
	bus.Publish(event.CallEnded{Call: call, Result: event.Result{Stdout: "the whole of it"}})

	bus.Drain(3 * time.Second)
	stop()

	got, err := s.Replay(session)
	require.NoError(t, err)
	require.Len(t, got, 2, "the header and the one fact worth replaying")
	assert.Equal(t, event.CallEndedKind, got[1].Event.Kind())
}

// Stopping must wait for the last write, not merely for the last
// receive: a session that loses its tail at exit cannot be resumed to
// where it actually got to.
func TestWatch_StopWaitsForTheLastWrite(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	bus := event.New()
	stop := store.Watch(bus, s, session)

	bus.Publish(event.SessionStarted{Session: session, Model: "m"})
	const n = 200
	for range n {
		bus.Publish(event.Notice{Text: "x", Level: "info"})
	}
	bus.Drain(5 * time.Second)
	stop()

	got, err := s.Replay(session)
	require.NoError(t, err)
	assert.Len(t, got, n+1, "everything published before the stop must be on disk")
}

// A database that cannot be written must not wedge the bus: the
// session carries on unrecorded rather than stopping.
func TestWatch_AnUnwritableStoreDoesNotBlockTheSession(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	bus := event.New()
	stop := store.Watch(bus, s, session)
	require.NoError(t, s.Close())

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 20 {
			bus.Publish(event.Notice{Text: "still going"})
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("publishing blocked on a broken store")
	}
	bus.Drain(time.Second)
	stop()
}

// Two processes, one session: the second must continue the ordinals
// rather than mint 1 again, or INSERT OR REPLACE silently overwrites
// what the first one recorded.
func TestWatch_AResumedSessionDoesNotOverwriteTheFirst(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())

	first := event.New()
	stopFirst := store.Watch(first, s, session)
	first.Publish(event.SessionStarted{Session: session, Model: "m"})
	first.Publish(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "before"})
	first.Publish(event.TurnEnded{Reason: event.EndDone})
	first.Drain(3 * time.Second)
	stopFirst()

	stored, err := s.Replay(session)
	require.NoError(t, err)
	require.Len(t, stored, 3)

	// The restart.
	second := event.New()
	second.Resume(stored[len(stored)-1].Ordinal)
	stopSecond := store.Watch(second, s, session)
	second.Publish(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 2, Prompt: "after"})
	second.Drain(3 * time.Second)
	stopSecond()

	got, err := s.Replay(session)
	require.NoError(t, err)
	require.Len(t, got, 4, "the new record joins the old ones rather than replacing one")

	var prompts []string
	for _, r := range got {
		if v, ok := r.Event.(event.TurnStarted); ok {
			prompts = append(prompts, v.Prompt)
		}
	}
	assert.Equal(t, []string{"before", "after"}, prompts, "both Turns survive, in order")
}
