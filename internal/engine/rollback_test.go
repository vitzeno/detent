package engine

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/model"
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
	afterFirst := len(r.eng.Transcript())
	r.run("second request")
	require.Greater(t, len(r.eng.Transcript()), afterFirst)

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

	assert.Len(t, r.eng.Transcript(), afterFirst, "the transcript rewinds to where that prompt landed")
	wellFormed(t, r.eng.Transcript())
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
	assert.Empty(t, r.eng.Transcript())
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
