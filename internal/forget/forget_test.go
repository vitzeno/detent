package forget

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

func TestForget_DeletesAndSaysSo(t *testing.T) {
	store := &fakeSessions{gone: true}
	var mu sync.Mutex
	var removed []string
	r := start(t, store, uuid.Must(uuid.NewV7()),
		WithContainers(func(_ context.Context, id string) error {
			mu.Lock()
			defer mu.Unlock()
			removed = append(removed, id)
			return nil
		}))

	target := uuid.Must(uuid.NewV7())
	r.bus.Publish(event.DeleteSession{Session: target})

	n := r.notice(t)
	assert.Equal(t, "info", n.Level)
	assert.Contains(t, n.Text, target.String())
	assert.Equal(t, []uuid.UUID{target}, store.calls())
	mu.Lock()
	assert.Equal(t, []string{target.String()}, removed, "the container stayed")
	mu.Unlock()

	select {
	case <-r.relisted:
	case <-time.After(2 * time.Second):
		t.Fatal("the list was never refreshed, so it still shows a deleted session")
	}
}

// The running session has its store, log and container all open. It
// is the one thing that cannot go.
func TestForget_RefusesTheRunningSession(t *testing.T) {
	store := &fakeSessions{gone: true}
	current := uuid.Must(uuid.NewV7())
	r := start(t, store, current)

	r.bus.Publish(event.DeleteSession{Session: current})

	n := r.notice(t)
	assert.Equal(t, "error", n.Level)
	assert.Contains(t, n.Text, "running session")
	assert.Empty(t, store.calls(), "it reached the store anyway")
}

// A typo should not read as success.
func TestForget_SaysWhenThereWasNothingThere(t *testing.T) {
	r := start(t, &fakeSessions{gone: false}, uuid.Must(uuid.NewV7()))
	missing := uuid.Must(uuid.NewV7())

	r.bus.Publish(event.DeleteSession{Session: missing})

	n := r.notice(t)
	assert.Equal(t, "error", n.Level)
	assert.Contains(t, n.Text, missing.String())
}

func TestForget_ReportsAStoreThatRefused(t *testing.T) {
	r := start(t, &fakeSessions{err: errors.New("database is locked")}, uuid.Must(uuid.NewV7()))
	r.bus.Publish(event.DeleteSession{Session: uuid.Must(uuid.NewV7())})

	n := r.notice(t)
	assert.Equal(t, "error", n.Level)
	assert.Contains(t, n.Text, "locked")
}

// The session is already gone by then, so a container that stayed is
// a leak worth mentioning rather than a failure to undo.
func TestForget_AContainerThatStaysIsStillADelete(t *testing.T) {
	store := &fakeSessions{gone: true}
	r := start(t, store, uuid.Must(uuid.NewV7()),
		WithContainers(func(context.Context, string) error { return errors.New("containerd is down") }))
	r.bus.Publish(event.DeleteSession{Session: uuid.Must(uuid.NewV7())})

	n := r.notice(t)
	assert.Contains(t, n.Text, "container stayed")
	require.Len(t, store.calls(), 1, "the delete itself should still have happened")
}

// Another detent with the session open is the one thing the container
// can tell, so the events must still be there when it says so.
func TestForget_RefusesASessionOpenElsewhere(t *testing.T) {
	store := &fakeSessions{gone: true}
	r := start(t, store, uuid.Must(uuid.NewV7()),
		WithContainers(func(context.Context, string) error {
			return fmt.Errorf("%w: sandbox: session is already running somewhere", ErrLive)
		}))
	r.bus.Publish(event.DeleteSession{Session: uuid.Must(uuid.NewV7())})

	n := r.notice(t)
	assert.Equal(t, "error", n.Level)
	assert.Contains(t, n.Text, "another detent")
	assert.Empty(t, store.calls(), "its history went while it was open")
}

// Stop must wait for a delete in flight, or it runs on against a store
// the caller is about to close.
func TestWatch_StopWaitsForADeleteInFlight(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	store := &fakeSessions{gone: true}
	stop := Watch(bus, store, uuid.Must(uuid.NewV7()),
		WithContainers(func(context.Context, string) error {
			close(entered)
			<-release
			return nil
		}))
	bus.Publish(event.DeleteSession{Session: uuid.Must(uuid.NewV7())})
	<-entered

	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("stop returned with a delete still running")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-stopped
	assert.Len(t, store.calls(), 1)
}

// No sandbox this run, by omitting the option or passing nil, means
// nothing to remove rather than a nil call.
func TestForget_NoSandboxIsNotAFailure(t *testing.T) {
	for name, opts := range map[string][]Option{
		"no option":           nil,
		"WithContainers(nil)": {WithContainers(nil)},
	} {
		t.Run(name, func(t *testing.T) {
			r := start(t, &fakeSessions{gone: true}, uuid.Must(uuid.NewV7()), opts...)
			r.bus.Publish(event.DeleteSession{Session: uuid.Must(uuid.NewV7())})

			assert.Equal(t, "info", r.notice(t).Level)
		})
	}
}

func TestForget_NoStoreSaysSo(t *testing.T) {
	r := start(t, nil, uuid.Must(uuid.NewV7()))
	r.bus.Publish(event.DeleteSession{Session: uuid.Must(uuid.NewV7())})

	n := r.notice(t)
	assert.Equal(t, "error", n.Level)
	assert.Contains(t, n.Text, "nothing is recording")
}

// fakeSessions stands in for the store, so the one path that destroys
// things is testable without anything to destroy.
type fakeSessions struct {
	mu      sync.Mutex
	deleted []uuid.UUID
	gone    bool
	err     error
}

func (f *fakeSessions) Delete(id uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	return f.gone, f.err
}

func (f *fakeSessions) calls() []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uuid.UUID(nil), f.deleted...)
}

// rig wires a watcher to a bus and collects what it says.
type rig struct {
	bus      *event.Bus
	notices  chan event.Notice
	relisted chan struct{}
}

func start(t *testing.T, sessions Sessions, current uuid.UUID, opts ...Option) *rig {
	t.Helper()
	bus := event.New()
	facts, unsub := bus.Subscribe(event.Only(event.NoticeKind, event.ListSessionsKind))
	stop := Watch(bus, sessions, current, opts...)
	t.Cleanup(func() { stop(); unsub(); bus.Close() })

	r := &rig{bus: bus, notices: make(chan event.Notice, 8), relisted: make(chan struct{}, 8)}
	go func() {
		for rec := range facts {
			switch e := rec.Event.(type) {
			case event.Notice:
				r.notices <- e
			case event.ListSessions:
				r.relisted <- struct{}{}
			}
		}
	}()
	return r
}

func (r *rig) notice(t *testing.T) event.Notice {
	t.Helper()
	select {
	case n := <-r.notices:
		return n
	case <-time.After(2 * time.Second):
		t.Fatal("nothing was said about the delete")
		return event.Notice{}
	}
}
