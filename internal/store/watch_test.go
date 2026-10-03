package store_test

import (
	"sync/atomic"
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
		event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "ls"}},
		event.ToolCallEnded{ToolCall: call, Result: event.Result{Stdout: "a.go\n"}},
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

// Live output is the one fact a replay has no use for: a replayed tool call
// has already finished, so there is nothing to redraw.
func TestWatch_SkipsLiveOutputAndIntents(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	bus := event.New()
	stop := store.Watch(bus, s, session)

	bus.Publish(event.SessionStarted{Session: session, Model: "m"})
	call := uuid.Must(uuid.NewV7())
	for range 50 {
		bus.Publish(event.OutputChunk{ToolCall: call, Line: "noise"})
	}
	bus.Publish(event.SubmitPrompt{Text: "an intent"})
	bus.Publish(event.Abort{Turn: uuid.Must(uuid.NewV7())})
	bus.Publish(event.ToolCallEnded{ToolCall: call, Result: event.Result{Stdout: "the whole of it"}})

	bus.Drain(3 * time.Second)
	stop()

	got, err := s.Replay(session)
	require.NoError(t, err)
	require.Len(t, got, 2, "the header and the one fact worth replaying")
	assert.Equal(t, event.ToolCallEndedKind, got[1].Event.Kind())
}

// Stopping must wait for the last write, not the last receive, or a
// session loses its tail at exit.
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

// Only the log heard about a failed write, so the human resumed a
// session with holes. The first failure is said on screen, once.
func TestWatch_AFailedWriteIsSaidOnce(t *testing.T) {
	s := open(t)
	bus := event.New()
	var warned atomic.Int32
	defer bus.Handle(event.Only(event.NoticeKind), func(r event.Record) {
		if r.Event.(event.Notice).Level == "warn" {
			warned.Add(1)
		}
	})()
	stop := store.Watch(bus, s, uuid.Must(uuid.NewV7()))
	require.NoError(t, s.Close())

	for range 5 {
		bus.Publish(event.TurnEnded{Reason: event.EndDone})
	}
	// The store settles and stops first, so its warnings are all out before they are counted.
	bus.Settle(3 * time.Second)
	stop()
	bus.Settle(3 * time.Second)
	assert.EqualValues(t, 1, warned.Load())
}

// A second process must continue the ordinals rather than mint 1 again,
// or INSERT OR REPLACE overwrites what the first recorded.
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

// A name is the one thing in the header a human writes, so it has to
// survive the session being picked up again.
func TestWatch_RenameSticksAcrossAResume(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	bus := event.New()
	stop := store.Watch(bus, s, session)
	defer stop()

	listed, unsub := bus.Subscribe(event.Only(event.SessionsListedKind))
	defer unsub()

	bus.Publish(event.SessionStarted{Session: session, Model: "m"})
	bus.Publish(event.RenameSession{Session: session, Name: "the sandbox bug"})

	select {
	case rec := <-listed:
		got := rec.Event.(event.SessionsListed).Sessions
		require.Len(t, got, 1)
		assert.Equal(t, "the sandbox bug", got[0].Name, "and it answers with the new listing")
	case <-time.After(3 * time.Second):
		t.Fatal("a rename went unanswered")
	}

	// The resume: a second SessionStarted for the same session.
	bus.Publish(event.SessionStarted{Session: session, Model: "m", Resumed: 2})
	bus.Drain(3 * time.Second)
	stop()

	all, err := s.Sessions()
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "the sandbox bug", all[0].Name,
		"a later run describes itself, it does not rename itself")
}

// A rename says whether it took, because only the store knows.
func TestWatch_RenameSaysWhetherItTook(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	bus := event.New()
	stop := store.Watch(bus, s, session)
	defer stop()

	said, unsub := bus.Subscribe(event.Only(event.NoticeKind))
	defer unsub()
	bus.Publish(event.SessionStarted{Session: session, Model: "m"})

	next := func(t *testing.T) event.Notice {
		t.Helper()
		select {
		case rec := <-said:
			return rec.Event.(event.Notice)
		case <-time.After(3 * time.Second):
			t.Fatal("a rename went unanswered")
			return event.Notice{}
		}
	}

	bus.Publish(event.RenameSession{Session: session, Name: "the sandbox bug"})
	got := next(t)
	assert.Equal(t, "info", got.Level)
	assert.Contains(t, got.Text, "the sandbox bug")

	bus.Publish(event.RenameSession{Session: session, Name: store.ReservedName})
	got = next(t)
	assert.Equal(t, "error", got.Level)
	assert.Contains(t, got.Text, "newest session")
}
