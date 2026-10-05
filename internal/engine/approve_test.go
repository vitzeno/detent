package engine

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/tool"
)

// Root approvals are serial today, so these stand in for the Turn goroutine
// and ask from several at once, as subagents will.

func TestApproval_ReachesItsCallWhileAnotherWaits(t *testing.T) {
	e, tr, ctx := idleTurn(t)
	first, second := asking(ctx, e, tr), asking(ctx, e, tr)
	waitingFor(t, tr, 2)

	e.dispatch(ctx, event.ResolveApproval{ToolCall: second.id, Approved: true}, nil)
	e.dispatch(ctx, event.ResolveApproval{ToolCall: first.id, Approved: false}, nil)

	assert.Equal(t, approved, second.verdict(t))
	assert.Equal(t, declined, first.verdict(t))
}

// An answer skips the inbox, which nobody reads while the Turn waits on a
// parallel batch and which a burst of notes can fill.
func TestApproval_ReachesItsWaiterWhenTheInboxIsFull(t *testing.T) {
	e, tr, ctx := idleTurn(t)
	for range cap(tr.inbox) {
		tr.inbox <- event.NoteContext{Text: "busy"}
	}
	call := uuid.Must(uuid.NewV7())
	answer, forget := tr.await(call)
	defer forget()

	e.dispatch(ctx, event.ResolveApproval{ToolCall: call, Approved: true}, nil)

	select {
	case ok := <-answer:
		assert.True(t, ok)
	case <-time.After(time.Second):
		t.Fatal("the answer never arrived")
	}
}

func TestApproval_AbortEndsAWaitingQuestion(t *testing.T) {
	e, tr, ctx := idleTurn(t)
	first, second := asking(ctx, e, tr), asking(ctx, e, tr)
	waitingFor(t, tr, 2)

	e.dispatch(ctx, event.Abort{}, nil)

	assert.Equal(t, abandoned, first.verdict(t))
	assert.Equal(t, abandoned, second.verdict(t))
}

// Notes typed while the human takes minutes to answer would otherwise
// pass the inbox's 64 and be dropped.
func TestApproval_RootKeepsDrainingTheInboxWhileItWaits(t *testing.T) {
	e, tr, ctx := idleTurn(t)
	q := asking(ctx, e, tr)
	waitingFor(t, tr, 1)

	const notes = 100
	for range notes {
		e.dispatch(ctx, event.NoteContext{Text: "and also"}, nil)
		require.Eventually(t, func() bool { return len(tr.inbox) == 0 }, time.Second, time.Millisecond)
	}
	e.dispatch(ctx, event.ResolveApproval{ToolCall: q.id, Approved: true}, nil)

	assert.Equal(t, approved, q.verdict(t))
	assert.Len(t, tr.takeNotes(), notes)
}

// idleTurn is an engine with a Turn in flight that no goroutine runs, so the
// test can wait on its questions itself.
func idleTurn(t *testing.T) (*Engine, *turnState, context.Context) {
	t.Helper()
	bus := event.New()
	t.Cleanup(bus.Close)
	e := New(bus, &fakeModel{}, tool.Standard(), fakeSelector{&fakeRunner{}})
	ctx, cancel := context.WithCancel(t.Context())
	tr := newTurnState(1, "go", cancel)
	e.cur = tr
	return e, tr, ctx
}

// question is one approve running on its own goroutine.
type question struct {
	id  uuid.UUID
	got chan approval
}

func asking(ctx context.Context, e *Engine, tr *turnState) *question {
	q := &question{id: uuid.Must(uuid.NewV7()), got: make(chan approval, 1)}
	p := &toolCallPlan{id: q.id, call: bashCall(q.id.String(), "rm -rf build")}
	go func() { q.got <- e.approve(ctx, tr, e.root, p) }()
	return q
}

func (q *question) verdict(t *testing.T) approval {
	t.Helper()
	select {
	case a := <-q.got:
		return a
	case <-time.After(3 * time.Second):
		t.Fatal("the question was never resolved")
		return 0
	}
}

// waitingFor blocks until n questions are registered, so an answer is not
// sent before anyone could take it.
func waitingFor(t *testing.T, tr *turnState, n int) {
	t.Helper()
	require.Eventually(t, func() bool {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		return len(tr.waiting) == n
	}, 3*time.Second, time.Millisecond)
}
