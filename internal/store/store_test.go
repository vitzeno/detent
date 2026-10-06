package store_test

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/store"
)

// The gate: a record written now must come back later as the same
// record, for every kind there is.
func TestStore_RoundTripsEveryKind(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	shell := uuid.Must(uuid.NewV7())

	want := []event.Record{
		rec(1, event.SessionStarted{Session: session, Model: "m", MaxSteps: 50}),
		rec(2, event.TurnStarted{Turn: turn, N: 1, Prompt: "go"}),
		rec(3, event.Appended{Turn: turn, Messages: []event.Message{
			{Role: event.RoleUser, Content: "go"}}}),
		rec(4, event.ToolCallProposed{ToolCall: call, Tool: "bash",
			Args: map[string]any{"command": "ls"}}),
		rec(5, event.ToolCallEnded{ToolCall: call, Took: 12 * time.Millisecond,
			Result: event.Result{ExitCode: 1, Stderr: "boom"}}),
		rec(6, event.Compacted{Turn: turn, Dropped: 4, Note: "summary"}),
		rec(7, event.TurnEnded{Turn: turn, Reason: event.EndDone,
			Usage: event.Usage{PromptTokens: 9, Latency: time.Second}}),
		rec(8, event.UserCommandStarted{UserCommand: shell, Command: "git status", Runner: "sandbox"}),
		rec(9, event.UserCommandEnded{UserCommand: shell, Took: 8 * time.Millisecond,
			Result: event.Result{Stdout: "clean"}}),
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

// The one picked up most recently comes first, however long ago it began.
func TestStore_ListsSessionsMostRecentlyUsedFirst(t *testing.T) {
	s := open(t)
	older, newer := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	require.NoError(t, s.Append(older, event.Record{Ordinal: 1,
		At: time.UnixMilli(1_000), Event: event.SessionStarted{Session: older}}))
	require.NoError(t, s.Append(newer, event.Record{Ordinal: 1,
		At: time.UnixMilli(5_000), Event: event.SessionStarted{Session: newer}}))
	require.NoError(t, s.Append(older, event.Record{Ordinal: 2,
		At: time.UnixMilli(9_000), Event: event.Notice{Text: "resumed"}}))

	got, err := s.Sessions()
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, older, got[0].ID, "used last, so first")
	assert.Equal(t, time.UnixMilli(9_000).UTC(), got[0].Used)
	assert.Equal(t, time.UnixMilli(1_000).UTC(), got[0].Started, "and it still says when it began")
}

func TestStore_ListsSessionsNewestFirst(t *testing.T) {
	s := open(t)
	older, newer := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	require.NoError(t, s.Append(older, event.Record{Ordinal: 1,
		At: time.UnixMilli(1_000), Event: event.SessionStarted{Session: older, Model: "old-model"}}))
	require.NoError(t, s.Append(older, event.Record{Ordinal: 2, At: time.UnixMilli(2_000), Event: event.Notice{Text: "b"}}))
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

// The events row has a foreign key, so a log without its header is
// refused by the database rather than discovered by a reader.
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
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := store.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// A trigger refuses the event row, so the failure lands after the header.
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec(`CREATE TRIGGER refuse BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT, 'refused'); END`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	session := uuid.Must(uuid.NewV7())
	require.ErrorContains(t, s.Append(session, rec(1, event.SessionStarted{Session: session})), "refused")
	got, err := s.Sessions()
	require.NoError(t, err)
	assert.Empty(t, got, "the header went back with the event")
}

// Two detents on one file is the ordinary case, so a second writer
// waits its turn rather than losing records to SQLITE_BUSY.
func TestStore_TwoProcessesWriteOneFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	stores := make([]*store.Store, 2)
	for i := range stores {
		s, err := store.Open(path)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		stores[i] = s
	}
	sessions := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}

	const each = 100
	var wg sync.WaitGroup
	errs := make(chan error, 2*each)
	for i, s := range stores {
		wg.Go(func() {
			if err := s.Append(sessions[i], rec(1, event.SessionStarted{Session: sessions[i]})); err != nil {
				errs <- err
				return
			}
			for n := uint64(2); n <= each; n++ {
				if err := s.Append(sessions[i], rec(n, event.Notice{Text: "x"})); err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	for i, session := range sessions {
		got, err := stores[1-i].Replay(session)
		require.NoError(t, err)
		assert.Len(t, got, each, "every record of session %d arrived", i)
	}
}

func TestOpen_RefusesNoPath(t *testing.T) {
	_, err := store.Open("")
	assert.Error(t, err, "an empty path would record into a temporary database nobody can resume")
}

// -resume resolves an id, then "last", then a name, so a name colliding
// with either could never be typed and is refused when set.
func TestRename_RefusesANameNobodyCouldUse(t *testing.T) {
	s := open(t)
	session := uuid.Must(uuid.NewV7())
	begin(t, s, session)

	tests := []struct {
		name string
		want string
	}{
		{"last", "most recently used session"},
		{"LAST", "most recently used session"},
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
	require.ErrorContains(t, s.Rename(second, "the sandbox bug"), "already called")

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

func TestRename_SaysWhenThereIsNoSuchSession(t *testing.T) {
	s := open(t)
	assert.ErrorContains(t, s.Rename(uuid.Must(uuid.NewV7()), "a name"), "no session")
}

// The foreign key cascades: a session that lists as gone leaves
// nothing behind.
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

// The cascade rests on foreign keys, which SQLite enables per connection,
// so a delete through a fresh one must still take the events with it.
func TestDelete_CascadesThroughAnyConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	writer, err := store.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Close() })
	session := uuid.Must(uuid.NewV7())
	begin(t, writer, session)
	require.NoError(t, writer.Append(session, rec(2, event.Notice{Text: "x"})))

	deleter, err := store.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = deleter.Close() })
	gone, err := deleter.Delete(session)
	require.NoError(t, err)
	require.True(t, gone)

	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	var left int
	require.NoError(t, raw.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&left))
	assert.Zero(t, left, "no event outlives its session")
}

// Not an error, but worth saying: a typo should not read as success.
func TestDelete_SaysWhenThereWasNothingToDelete(t *testing.T) {
	s := open(t)
	gone, err := s.Delete(uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	assert.False(t, gone)
}

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

// begin writes the header, which events reference, as ordinal 1 like a
// real session's SessionStarted.
func begin(t *testing.T, s *store.Store, session uuid.UUID) {
	t.Helper()
	require.NoError(t, s.Append(session, rec(1,
		event.SessionStarted{Session: session, Model: "m", Recorded: true})))
}
