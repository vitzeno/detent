package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/store"
)

func open(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "events.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func rec(n uint64, e event.Event) event.Record {
	return event.Record{Ordinal: n, At: time.UnixMilli(1_700_000_000_000 + int64(n)), Event: e}
}

// The gate: a record written now must come back later as the same
// record, for every kind there is.
func TestStore_RoundTripsEveryKind(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	want := []event.Record{
		rec(1, event.SessionStarted{Session: session, Model: "m", MaxSteps: 50}),
		rec(2, event.TurnStarted{Turn: turn, N: 1, Prompt: "go"}),
		rec(3, event.Appended{Turn: turn, Messages: []event.Message{
			{Role: event.RoleUser, Content: "go"}}}),
		rec(4, event.CallProposed{Call: call, Tool: "bash",
			Args: map[string]any{"command": "ls"}}),
		rec(5, event.CallEnded{Call: call, Took: 12 * time.Millisecond,
			Result: event.Result{ExitCode: 1, Stderr: "boom"}}),
		rec(6, event.Compacted{Turn: turn, Dropped: 4, Note: "summary"}),
		rec(7, event.TurnEnded{Turn: turn, Reason: event.EndDone,
			Usage: event.Usage{PromptTokens: 9, Latency: time.Second}}),
	}
	for _, r := range want {
		require.NoError(t, s.Append(session, r))
	}

	got, err := s.Replay(session)
	require.NoError(t, err)
	require.Len(t, got, len(want))
	for i := range want {
		assert.Equal(t, want[i].Ordinal, got[i].Ordinal)
		assert.True(t, want[i].At.Equal(got[i].At), "the time survives")
		assert.Equal(t, want[i].Event, got[i].Event, "record %d", i)
	}
}

// Replay is ordered by ordinal, not by insertion, because a subscriber
// may write out of order and the log is the record of what happened.
func TestStore_ReplaysInOrdinalOrder(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	for _, n := range []uint64{5, 1, 4, 2, 3} {
		require.NoError(t, s.Append(session, rec(n, event.Notice{Text: "x"})))
	}
	got, err := s.Replay(session)
	require.NoError(t, err)

	require.Len(t, got, 5)
	for i, r := range got {
		assert.EqualValues(t, i+1, r.Ordinal)
	}
}

func TestStore_KeepsSessionsApart(t *testing.T) {
	s := open(t)
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	require.NoError(t, s.Append(a, rec(1, event.Notice{Text: "mine"})))
	require.NoError(t, s.Append(b, rec(1, event.Notice{Text: "theirs"})))

	got, err := s.Replay(a)
	require.NoError(t, err)
	require.Len(t, got, 1, "an ordinal is per session, so both rows are ordinal 1")
	assert.Equal(t, "mine", got[0].Event.(event.Notice).Text)
}

// Writing the same ordinal twice is the same row: a store that
// duplicated would make replay disagree with what the bus published.
func TestStore_AppendIsIdempotent(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	r := rec(1, event.Notice{Text: "once"})
	require.NoError(t, s.Append(session, r))
	require.NoError(t, s.Append(session, r))

	got, err := s.Replay(session)
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

// Undoing a Turn is truncating the log, the same thing it means in
// memory.
func TestStore_TruncateDropsWhatCameAfter(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	for n := uint64(1); n <= 6; n++ {
		require.NoError(t, s.Append(session, rec(n, event.Notice{Text: "x"})))
	}
	require.NoError(t, s.Truncate(session, 3))

	got, err := s.Replay(session)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.EqualValues(t, 3, got[len(got)-1].Ordinal)
}

func TestStore_ListsSessionsNewestFirst(t *testing.T) {
	s := open(t)
	older, newer := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	require.NoError(t, s.Append(older, rec(1, event.Notice{Text: "a"})))
	require.NoError(t, s.Append(older, rec(2, event.Notice{Text: "b"})))
	require.NoError(t, s.Append(newer, rec(9, event.Notice{Text: "c"})))

	got, err := s.Sessions()
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, newer, got[0].ID, "newest first")
	assert.Equal(t, older, got[1].ID)
	assert.Equal(t, 2, got[1].Events)
	assert.False(t, got[0].Started.IsZero())
}

// A resumed session mints ordinals from 1 again, so it has to pick up
// where the stored ones left off or the new rows replace the old.
func TestStore_ReplayShowsWhereToResumeFrom(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	for n := uint64(1); n <= 4; n++ {
		require.NoError(t, s.Append(session, rec(n, event.Notice{Text: "x"})))
	}
	got, err := s.Replay(session)
	require.NoError(t, err)
	assert.EqualValues(t, 4, got[len(got)-1].Ordinal,
		"the last ordinal is what a resumed bus must continue from")
}

func TestStore_EmptyReplayIsNotAnError(t *testing.T) {
	s := open(t)
	got, err := s.Replay(uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	assert.Empty(t, got)
}
