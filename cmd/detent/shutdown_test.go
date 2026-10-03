package main

import (
	"bytes"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/store"
)

// The engine stops and publishes what its abandoned Turn owes before the
// bus drains. Draining first swallows those facts and breaks resume.
func TestShutdown_DrainsOnlyAfterTheEngineHasStopped(t *testing.T) {
	bus := event.New()
	facts, unsub := bus.Subscribe(event.Facts())
	t.Cleanup(unsub)

	var mu sync.Mutex
	var got []event.Kind
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for rec := range facts {
			mu.Lock()
			got = append(got, rec.Event.Kind())
			mu.Unlock()
		}
	}()

	// Stands in for the Turn unwinding: it publishes what it owes and
	// only then reports the engine stopped.
	stopped, engine := make(chan struct{}), make(chan struct{})
	go func() {
		<-stopped
		bus.Publish(event.TurnEnded{Reason: event.EndAborted})
		close(engine)
	}()

	sd := shutdown{bus: bus, engine: engine, stop: func() { close(stopped) }}
	sd.close()
	<-collected

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, got, event.TurnEndedKind, "the last fact was lost: the bus closed before the engine stopped")
}

// The same rule one step earlier: the command's end is published before
// the engine goes, or the message it owes the transcript lands nowhere.
func TestShutdown_StopsTheUserCommandBeforeTheEngineAndTheDrain(t *testing.T) {
	bus := event.New()
	records, unsub := bus.Subscribe(nil)
	t.Cleanup(unsub)

	var mu sync.Mutex
	var got []event.Kind
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for rec := range records {
			mu.Lock()
			got = append(got, rec.Event.Kind())
			mu.Unlock()
		}
	}()

	// Stands in for what usercommand.Watch returns: it publishes what
	// the cancelled command owes and only then returns.
	shell := func() {
		bus.Publish(event.UserCommandEnded{Result: event.Result{Err: "stopped by the human"}})
		bus.Publish(event.NoteContext{Text: "[human ran a command on the host]"})
	}
	stopped, engine := make(chan struct{}), make(chan struct{})
	go func() {
		<-stopped
		bus.Publish(event.TurnEnded{Reason: event.EndAborted})
		close(engine)
	}()

	sd := shutdown{bus: bus, userCommand: shell, engine: engine, stop: func() { close(stopped) }}
	sd.close()
	<-collected

	mu.Lock()
	defer mu.Unlock()
	require.Contains(t, got, event.UserCommandEndedKind, "the command's end was lost to the drain")
	require.Contains(t, got, event.NoteContextKind, "and so was the message it owes the model")
	assert.Less(t, slices.Index(got, event.UserCommandEndedKind), slices.Index(got, event.TurnEndedKind),
		"the shell stops first, or its note reaches an engine that has already gone")
}

// An engine that will not stop must not hold the terminal: it is given
// a bound and the human is told the transcript may be short.
func TestShutdown_GivesUpOnAnEngineThatWillNotStop(t *testing.T) {
	var out bytes.Buffer
	sd := shutdown{engine: make(chan struct{}), grace: 50 * time.Millisecond} // never closed
	done := make(chan []error, 1)
	go func() { done <- sd.stopEngine() }()

	select {
	case errs := <-done:
		require.Len(t, errs, 1)
		sd.report(&out, errs)
		assert.Contains(t, out.String(), "did not stop in 50ms")
		assert.Contains(t, out.String(), "last steps may be missing")
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown hung on an engine that never stopped")
	}
}

// The line a human reads once the screen is back. The id is the point
// of it: -resume last is ambiguous the moment another session starts.
func TestShutdown_ReportsHowItWentAndHowToResume(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	tests := []struct {
		name string
		sd   shutdown
		errs []error
		want []string
		gone []string
	}{
		{
			name: "a session that was recorded",
			sd:   shutdown{session: id, bus: event.New(), events: &store.Store{}},
			want: []string{"session saved", "-resume " + id.String()},
		},
		{
			name: "a session nothing was writing down",
			sd:   shutdown{session: id, bus: event.New()},
			want: []string{"not recorded"},
			gone: []string{"-resume"},
		},
		{
			name: "something did not close",
			sd:   shutdown{session: id, bus: event.New(), events: &store.Store{}},
			errs: []error{errors.New("mcp servers did not close in 5s")},
			want: []string{"mcp servers did not close", "-resume " + id.String()},
		},
		{
			name: "-sessions, -prune and -mcp, which ran no session",
			sd:   shutdown{session: id},
			gone: []string{"session", "-resume"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			tt.sd.report(&out, tt.errs)
			for _, w := range tt.want {
				assert.Contains(t, out.String(), w)
			}
			for _, g := range tt.gone {
				assert.NotContains(t, out.String(), g)
			}
		})
	}
}

// Invokers.Close is serial and the SDK gives each server ten seconds,
// so the deadline is what keeps a few slow ones from holding the terminal.
func TestBounded_ReturnsWhenTheCloseWillNot(t *testing.T) {
	never := make(chan struct{})
	t.Cleanup(func() { close(never) })
	err := bounded(50*time.Millisecond, "mcp servers", func() error {
		<-never
		return nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mcp servers did not close")

	require.NoError(t, bounded(time.Second, "mcp servers", func() error { return nil }))
	assert.EqualError(t, bounded(time.Second, "mcp servers", func() error { return errors.New("boom") }), "boom")
}

// Servers must not close mid-dial, or one that lands after Close is a
// process nothing owns. Bounded, since the dial's context is cancelled.
func TestWait_HoldsForTheBackgroundDialButNotForever(t *testing.T) {
	settled := make(chan struct{})
	close(settled)
	require.NoError(t, wait(settled, time.Second, "mcp servers finished connecting"))

	err := wait(make(chan struct{}), 50*time.Millisecond, "mcp servers finished connecting")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gave up after 50ms")
}
