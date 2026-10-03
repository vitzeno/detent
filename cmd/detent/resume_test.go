package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/store"
)

func TestResolveSession_ReadsAnIdThenLastThenAName(t *testing.T) {
	events, err := store.Open(filepath.Join(t.TempDir(), "events.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = events.Close() })

	_, err = resolveSession(events, "last")
	require.ErrorContains(t, err, "no sessions")

	older, newer := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for i, id := range []uuid.UUID{older, newer} {
		require.NoError(t, events.Append(id, event.Record{
			Ordinal: 1, At: time.Now().Add(time.Duration(i) * time.Second), Event: event.SessionStarted{Session: id}}))
	}
	require.NoError(t, events.Rename(older, "Mine"))

	for want, got := range map[uuid.UUID]string{older: "mine", newer: "LAST"} {
		id, err := resolveSession(events, got)
		require.NoError(t, err, got)
		assert.Equal(t, want, id, got)
	}
	id, err := resolveSession(events, newer.String())
	require.NoError(t, err)
	assert.Equal(t, newer, id)

	_, err = resolveSession(events, "nobody")
	assert.ErrorContains(t, err, `no session named "nobody"`)
}

// The note exists because the prompt tells the model its earlier
// steps are still on disk, and after a resume only some of them are.

func TestResumeNote_SaysWhatDidNotComeBack(t *testing.T) {
	sandbox := model.Environment{OS: "linux", Dir: "/workspace", Sandboxed: true}
	host := model.Environment{OS: "darwin", Dir: "/Users/x/proj"}

	cases := []struct {
		name  string
		was   bool
		env   model.Environment
		wants []string
		nots  []string
	}{
		{
			name: "container to container", was: true, env: sandbox,
			wants: []string{"container is gone", "/workspace, the human's own directory"},
			nots:  []string{"this session runs"},
		},
		{
			// Nothing was in a container, so nothing went with one.
			name: "host to container", was: false, env: sandbox,
			wants: []string{"this session runs in a container", "/workspace"},
			nots:  []string{"container is gone"},
		},
		{
			name: "container to host", was: true, env: host,
			wants: []string{"runs directly on the human's machine", "container is gone",
				"this machine's own filesystem"},
		},
		{
			name: "host to host", was: false, env: host,
			wants: []string{"this machine's own filesystem, including /Users/x/proj"},
			nots:  []string{"container"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resumeNote(started(c.was), c.env)
			assert.True(t, strings.HasPrefix(got, model.ResumeMarker+"\n"),
				"the prompt names this marker, so it opens the message")
			for _, w := range c.wants {
				assert.Contains(t, got, w)
			}
			for _, n := range c.nots {
				assert.NotContains(t, got, n)
			}
		})
	}
}

// Read off the header fact the session already recorded, so noticing
// a mode change costs no new state.
func TestWasSandboxed_ReadsTheLastHeader(t *testing.T) {
	was, known := wasSandboxed(nil)
	assert.False(t, known, "nothing recorded says nothing about the mode")
	assert.False(t, was)

	// A session resumed twice has two headers, and the last one ran.
	twice := append(started(true), started(false)...)
	was, known = wasSandboxed(twice)
	require.True(t, known)
	assert.False(t, was, "the most recent run is the one that set the machine up")
}

// The seam first: history draws the boundary, the note explains it.
func TestAnnounceResume_PublishesTheSeamThenTheNote(t *testing.T) {
	bus := event.New()
	seen, unsub := bus.Subscribe(nil)
	t.Cleanup(unsub)

	id := uuid.Must(uuid.NewV7())
	announceResume(bus, id, started(true), model.Environment{Dir: "/workspace", Sandboxed: true})

	first := next[event.SessionResumed](t, seen)
	assert.Equal(t, id, first.Session)
	assert.Equal(t, 1, first.Records)
	assert.True(t, first.Sandbox)

	second := next[event.NoteContext](t, seen)
	assert.Contains(t, second.Text, model.ResumeMarker)
}

// A fresh session has no seam and nothing to disclose.
func TestAnnounceResume_SaysNothingWithoutARestore(t *testing.T) {
	bus := event.New()
	seen, unsub := bus.Subscribe(nil)
	t.Cleanup(unsub)

	announceResume(bus, uuid.Must(uuid.NewV7()), nil, model.Environment{})
	bus.Publish(event.Notice{Text: "after"})

	next[event.Notice](t, seen) // the first thing on the bus is what came after it
}

// next is the following record, which must be a T, failing rather than hanging when none comes.
func next[T event.Event](t *testing.T, seen <-chan event.Record) T {
	t.Helper()
	select {
	case r := <-seen:
		got, ok := r.Event.(T)
		require.True(t, ok, "got %T", r.Event)
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was published")
	}
	var zero T
	return zero
}

func started(sandboxed bool) []event.Record {
	return []event.Record{{
		Ordinal: 1,
		Event:   event.SessionStarted{Session: uuid.Must(uuid.NewV7()), Sandbox: sandboxed},
	}}
}
