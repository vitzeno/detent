package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// A Turn is the unit of undo: its one checkpoint, and the transcript
// back to where the prompt landed.
func TestRollback_RestoresTheTurnsCheckpoint(t *testing.T) {
	snap := &snapRunner{fakeRunner: &fakeRunner{out: "ok\n"}}
	fm := &fakeModel{replies: []model.Reply{
		{Requests: []event.ToolRequest{bashCall("a", "one")}},
		{Text: "first done"},
		{Requests: []event.ToolRequest{bashCall("b", "two")}},
		{Text: "second done"},
	}}
	r := rigWith(t, event.New(), fm, snap)

	r.run("first request")
	afterFirst := len(r.eng.messages())
	r.run("second request")
	require.Greater(t, len(r.eng.messages()), afterFirst)

	turns := r.of(event.TurnStartedKind)
	require.Len(t, turns, 2)
	r.bus.Publish(event.RequestRollback{Turn: turns[1].(event.TurnStarted).Turn})

	back := r.await(event.RolledBackKind).(event.RolledBack)
	assert.Equal(t, turns[1].(event.TurnStarted).Turn, back.Turn)

	snap.mu.Lock()
	restored := append([]string(nil), snap.restored...)
	snap.mu.Unlock()
	require.Len(t, restored, 1, "one restore, to the second Turn's own checkpoint")
	assert.Equal(t, "snap-b", restored[0])

	r.dispatched()
	msgs := r.eng.messages()
	require.Len(t, msgs, afterFirst+1, "the transcript rewinds to where that prompt landed")
	assert.Contains(t, msgs[afterFirst].Content, "Request 2 was undone", "then says what may still be on disk")
	wellFormed(t, msgs)
}

func TestRollback_RefusesWhatItCannotDo(t *testing.T) {
	t.Run("an unknown turn", func(t *testing.T) {
		r := newRig(t, nil)
		r.bus.Publish(event.RequestRollback{Turn: uuid.Must(uuid.NewV7())})
		n := r.await(event.NoticeKind).(event.Notice)
		assert.Equal(t, "error", n.Level)
		assert.Contains(t, n.Text, "no such request")
	})

	t.Run("while a request is running", func(t *testing.T) {
		r := newRig(t, []model.Reply{{Requests: []event.ToolRequest{bashCall("a", "slow")}}})
		r.runner.mu.Lock()
		r.runner.hold = make(chan struct{})
		r.runner.mu.Unlock()

		r.bus.Publish(event.SubmitPrompt{Text: "go"})
		r.await(event.ToolCallStartedKind)
		r.bus.Publish(event.RequestRollback{Turn: uuid.Must(uuid.NewV7())})

		n := r.await(event.NoticeKind).(event.Notice)
		assert.Contains(t, n.Text, "cannot roll back while")
	})
}

// A rolled-back Turn did not happen, so it is no longer a target and
// the numbering picks up where it left off.
func TestRollback_ForgetsTheTurnAndEverythingAfter(t *testing.T) {
	snap := &snapRunner{fakeRunner: &fakeRunner{out: "ok\n"}}
	fm := &fakeModel{replies: []model.Reply{{Text: "a"}, {Text: "b"}, {Text: "c"}}}
	r := rigWith(t, event.New(), fm, snap)

	r.run("one")
	r.run("two")
	turns := r.of(event.TurnStartedKind)
	require.Len(t, turns, 2)

	first := turns[0].(event.TurnStarted).Turn
	r.bus.Publish(event.RequestRollback{Turn: first})
	r.await(event.RolledBackKind)

	// Rolling back the first should have dropped the second as well.
	r.bus.Publish(event.RequestRollback{Turn: turns[1].(event.TurnStarted).Turn})
	n := r.awaitNth(event.NoticeKind, 1).(event.Notice)
	assert.Contains(t, n.Text, "no such request")
	assert.Empty(t, r.eng.messages())
}

// TurnEnded is published a moment before the Turn's goroutine is done.
// An intent sent on seeing it must find the Turn over, not still running.
func TestTurn_AnIntentSentOnSeeingTheEndFindsTheTurnOver(t *testing.T) {
	snap := &snapRunner{fakeRunner: &fakeRunner{out: "ok\n"}}
	fm := &fakeModel{replies: []model.Reply{{Text: "a"}}}
	r := rigWith(t, event.New(), fm, snap)
	r.eng.lingerAfterTurn = func() { time.Sleep(50 * time.Millisecond) }

	turn := r.run("one").Turn
	r.bus.Publish(event.RequestRollback{Turn: turn})
	r.await(event.RolledBackKind)
}

// The summary cannot be cut in half, so a Turn inside it is refused
// before the sandbox is touched, rather than left half undone.
func TestRollback_RefusesATurnCompactedIntoTheSummary(t *testing.T) {
	r, snap, ids := compactedRig(t)
	before := len(r.of(event.NoticeKind))

	r.bus.Publish(event.RequestRollback{Turn: ids[0]})
	n := r.awaitNth(event.NoticeKind, before+1).(event.Notice)
	assert.Equal(t, "error", n.Level)
	assert.Contains(t, n.Text, "can no longer be undone")
	r.dispatched()

	assert.Empty(t, r.of(event.RolledBackKind))
	snap.mu.Lock()
	assert.Empty(t, snap.restored, "a refused undo must not restore the sandbox")
	snap.mu.Unlock()

	r.run("fourth request")
	sent := sentText(r.model.lastSent())
	for _, want := range []string{"earlier work", "second request", "third request", "fourth request"} {
		assert.Contains(t, sent, want)
	}
}

// A later Turn whose start survived compaction rewinds exactly, summary kept.
func TestRollback_UndoesATurnAfterTheSummary(t *testing.T) {
	r, snap, ids := compactedRig(t)

	r.bus.Publish(event.RequestRollback{Turn: ids[1]})
	r.await(event.RolledBackKind)
	snap.mu.Lock()
	assert.Equal(t, []string{"snap-b"}, snap.restored)
	snap.mu.Unlock()

	r.run("fourth request")
	sent := sentText(r.model.lastSent())
	assert.Contains(t, sent, "earlier work")
	assert.Contains(t, sent, "fourth request")
	for _, gone := range []string{"second request", "second done", "third request", "third done"} {
		assert.NotContains(t, sent, gone, "an undone Turn stayed in context")
	}
	wellFormed(t, r.model.lastSent())

	// A replay never compacts, so the same undo leaves it the first Turn whole.
	fresh := New(event.New(), &fakeModel{}, tool.Standard(), fakeSelector{&fakeRunner{}})
	defer fresh.unsub()
	fresh.Restore(r.records())
	restored := sentText(fresh.messages())
	assert.Contains(t, restored, "first request")
	assert.Contains(t, restored, "fourth request")
	assert.NotContains(t, restored, "second request")
	assert.NotContains(t, restored, "third request")
}

// compactedRig runs three Turns, and the second compacts the first into a summary.
func compactedRig(t *testing.T) (*rig, *snapRunner, []uuid.UUID) {
	t.Helper()
	snap := &snapRunner{fakeRunner: &fakeRunner{out: strings.Repeat("x", 4000) + "\n"}}
	fm := &fakeModel{replies: []model.Reply{
		{Requests: []event.ToolRequest{bashCall("a", "one")}},
		{Text: "first done"},
		{Text: "second done"},
		{Text: "third done"},
	}}
	r := rigWith(t, event.New(), fm, snap,
		WithContextTokens(200), WithSummarizer(stubSummarizer{out: "earlier work"}))
	r.run("first request")
	r.run("second request")
	r.run("third request")
	require.Len(t, r.of(event.CompactedKind), 1, "the test needs the first Turn compacted, and only it")

	var ids []uuid.UUID
	for _, ev := range r.of(event.TurnStartedKind) {
		ids = append(ids, ev.(event.TurnStarted).Turn)
	}
	require.Len(t, ids, 3)
	return r, snap, ids
}

func sentText(msgs []event.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content + "\n")
	}
	return b.String()
}
