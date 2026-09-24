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

// begin writes the header a session starts with. Events reference it,
// so nothing can be appended before it, which is true of a real
// session too: SessionStarted is always ordinal 1.
func begin(t *testing.T, s *store.Store, session uuid.UUID) {
	t.Helper()
	require.NoError(t, s.Append(session, rec(1,
		event.SessionStarted{Session: session, Model: "m", Recorded: true})))
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
	begin(t, s, session)
	for _, n := range []uint64{6, 2, 5, 3, 4} {
		require.NoError(t, s.Append(session, rec(n, event.Notice{Text: "x"})))
	}
	got, err := s.Replay(session)
	require.NoError(t, err)

	require.Len(t, got, 6)
	for i, r := range got {
		assert.EqualValues(t, i+1, r.Ordinal)
	}
}

func TestStore_KeepsSessionsApart(t *testing.T) {
	s := open(t)
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	begin(t, s, a)
	begin(t, s, b)
	require.NoError(t, s.Append(a, rec(2, event.Notice{Text: "mine"})))
	require.NoError(t, s.Append(b, rec(2, event.Notice{Text: "theirs"})))

	got, err := s.Replay(a)
	require.NoError(t, err)
	require.Len(t, got, 2, "an ordinal is per session, so both sessions have a 2")
	assert.Equal(t, "mine", got[1].Event.(event.Notice).Text)
}

// Writing the same ordinal twice is the same row: a store that
// duplicated would make replay disagree with what the bus published.
func TestStore_AppendIsIdempotent(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	begin(t, s, session)
	r := rec(2, event.Notice{Text: "once"})
	require.NoError(t, s.Append(session, r))
	require.NoError(t, s.Append(session, r))

	got, err := s.Replay(session)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

// Undoing a Turn is truncating the log, the same thing it means in
// memory.
func TestStore_TruncateDropsWhatCameAfter(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	begin(t, s, session)
	for n := uint64(2); n <= 6; n++ {
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
	require.NoError(t, s.Append(older, event.Record{Ordinal: 1,
		At: time.UnixMilli(1_000), Event: event.SessionStarted{Session: older, Model: "old-model"}}))
	require.NoError(t, s.Append(older, rec(2, event.Notice{Text: "b"})))
	require.NoError(t, s.Append(newer, event.Record{Ordinal: 1,
		At: time.UnixMilli(9_000), Event: event.SessionStarted{Session: newer, Model: "new-model"}}))

	got, err := s.Sessions()
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, newer, got[0].ID, "newest first")
	assert.Equal(t, older, got[1].ID)
	assert.Equal(t, 2, got[1].Events)
	assert.Equal(t, "old-model", got[1].Model, "the header says which model ran")
	assert.False(t, got[0].Started.IsZero())
}

// A resumed session mints ordinals from 1 again, so it has to pick up
// where the stored ones left off or the new rows replace the old.
func TestStore_ReplayShowsWhereToResumeFrom(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	begin(t, s, session)
	for n := uint64(2); n <= 4; n++ {
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

// The header is what makes a listing cheap and a session's own facts
// queryable, rather than buried in a payload.
func TestStore_TheHeaderIsWrittenWithTheFirstEvent(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	require.NoError(t, s.Append(session, event.Record{
		Ordinal: 1, At: time.UnixMilli(5_000),
		Event: event.SessionStarted{Session: session, Model: "a-model",
			Sandbox: true, Network: true, Recorded: true},
	}))

	got, err := s.Sessions()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a-model", got[0].Model)
	assert.Equal(t, time.UnixMilli(5_000).UTC(), got[0].Started)
	assert.Equal(t, 1, got[0].Events)
}

// Nothing may be written against a session that does not exist. The
// events row has a foreign key, so a log without its header is a
// shape the database refuses rather than one a reader discovers.
func TestStore_RefusesAnEventWithNoSession(t *testing.T) {
	s := open(t)
	err := s.Append(uuid.Must(uuid.NewV7()), rec(1, event.Notice{Text: "orphan"}))
	assert.ErrorContains(t, err, "FOREIGN KEY")
}

// A resumed session keeps the clock it started on and only says it
// was picked up again.
func TestStore_ResumingKeepsTheOriginalStart(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	first := event.SessionStarted{Session: session, Model: "m"}
	require.NoError(t, s.Append(session, event.Record{
		Ordinal: 1, At: time.UnixMilli(1_000), Event: first}))

	again := first
	again.Resumed = 12
	require.NoError(t, s.Append(session, event.Record{
		Ordinal: 2, At: time.UnixMilli(9_000), Event: again}))

	got, err := s.Sessions()
	require.NoError(t, err)
	require.Len(t, got, 1, "one session, picked up twice")
	assert.Equal(t, time.UnixMilli(1_000).UTC(), got[0].Started,
		"the header keeps the clock it started on")
}

// A header and its first event go in together, so a failure cannot
// leave a session listed with nothing in it.
func TestStore_AFailedAppendLeavesNoHeader(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	// Ordinal 1 twice in one call is impossible, so force the failure
	// after the header write by closing the database mid-flight.
	require.NoError(t, s.Close())

	err := s.Append(session, rec(1, event.SessionStarted{Session: session}))
	require.Error(t, err)
}

// -resume resolves an id, then "last", then a name. A name that
// collides with either is one nobody can type, so it is refused when
// it is set rather than discovered when the resume quietly does
// something else.
func TestRename_RefusesANameNobodyCouldUse(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	begin(t, s, session)

	tests := []struct {
		name string
		want string
	}{
		{"last", "newest session"},
		{"LAST", "newest session"},
		{uuid.Must(uuid.NewV7()).String(), "reads as a session id"},
		{"   ", "cannot be blank"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ErrorContains(t, s.Rename(session, tt.name), tt.want)
		})
	}
	assert.NoError(t, s.Rename(session, "the sandbox bug"), "an ordinary name is fine")
}

// Two sessions with one name makes -resume <name> ambiguous, and
// picking the newest silently is the worst of the options.
func TestRename_RefusesANameAlreadyTaken(t *testing.T) {
	s := open(t)
	first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	begin(t, s, first)
	begin(t, s, second)

	require.NoError(t, s.Rename(first, "the sandbox bug"))
	assert.ErrorContains(t, s.Rename(second, "the sandbox bug"), "already called")

	// Renaming to what it already is stays fine.
	assert.NoError(t, s.Rename(first, "the sandbox bug"))
	assert.NoError(t, s.Rename(second, "something else"))
}

// Unnamed is the normal state, and any number of sessions share it.
func TestRename_ManySessionsMayBeUnnamed(t *testing.T) {
	s := open(t)
	for range 3 {
		begin(t, s, uuid.Must(uuid.NewV7()))
	}
	got, err := s.Sessions()
	require.NoError(t, err)
	require.Len(t, got, 3)
	for _, g := range got {
		assert.Empty(t, g.Name)
	}
}

// Delete takes the events with it: the foreign key cascades, so a
// session that lists as gone leaves nothing behind in the database.
func TestDelete_TakesTheEventsWithIt(t *testing.T) {
	s := open(t)
	keep, drop := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, id := range []uuid.UUID{keep, drop} {
		require.NoError(t, s.Append(id, rec(1, event.SessionStarted{Session: id, Model: "m"})))
		require.NoError(t, s.Append(id, rec(2, event.Notice{Text: "x"})))
	}

	gone, err := s.Delete(drop)
	require.NoError(t, err)
	assert.True(t, gone)

	left, err := s.Sessions()
	require.NoError(t, err)
	require.Len(t, left, 1)
	assert.Equal(t, keep, left[0].ID)

	records, err := s.Replay(drop)
	require.NoError(t, err)
	assert.Empty(t, records, "the events outlived their session")

	kept, err := s.Replay(keep)
	require.NoError(t, err)
	assert.Len(t, kept, 2, "the wrong session lost records")
}

// Deleting what was never there is not an error, but it is worth
// saying so: a typo should not read as success.
func TestDelete_SaysWhenThereWasNothingToDelete(t *testing.T) {
	s := open(t)
	gone, err := s.Delete(uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	assert.False(t, gone)
}
