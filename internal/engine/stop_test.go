package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// A cancelled Run must not return before its Turn publishes what it
// owes: the caller closes the bus next, and an unrecorded Step cannot resume.
func TestRun_PublishesTheAbandonedTurnBeforeItReturns(t *testing.T) {
	const marker = "run returned"

	bus := event.New()
	t.Cleanup(bus.Close)

	seen := &kindLog{started: make(chan struct{}), arrived: make(chan struct{})}
	facts, unsub := bus.Subscribe(event.Facts())
	t.Cleanup(unsub)
	go seen.collect(facts, marker)

	runner := &fakeRunner{out: "output\n", hold: make(chan struct{})}
	eng := New(bus, &fakeModel{replies: []model.Reply{{Requests: []event.ToolRequest{bashCall("c1", "sleep 60")}}}},
		tool.Standard(), fakeSelector{runner})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); eng.Run(ctx) }()

	bus.Publish(event.SubmitPrompt{Text: "go"})
	require.True(t, waitFor(seen.started), "the call never started")

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run never returned")
	}
	// Published after Run returned, so anything the Turn owes must
	// already be ahead of it in the subscriber's queue.
	bus.Publish(event.Notice{Level: "info", Text: marker})
	require.True(t, waitFor(seen.arrived), "the marker never arrived")

	at, mark := seen.index()
	callEnded, ok := at[event.ToolCallEndedKind]
	require.True(t, ok, "the abandoned call was never answered at all")
	turnEnded, ok := at[event.TurnEndedKind]
	require.True(t, ok, "the turn never ended at all")
	assert.Less(t, callEnded, mark, "the abandoned call was answered after Run returned")
	assert.Less(t, turnEnded, mark, "the turn ended after Run returned")
}

// A cancelled Run with nothing running returns without waiting out the
// grace: quitting an idle session is instant.
func TestRun_IdleStopsWithoutWaiting(t *testing.T) {
	bus := event.New()
	t.Cleanup(bus.Close)

	eng := New(bus, &fakeModel{}, tool.Standard(), fakeSelector{&fakeRunner{}},
		WithStopGrace(30*time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); eng.Run(ctx) }()

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("an idle engine waited out the stop grace")
	}
}

// kindLog records what order facts arrived in, which is how a test
// tells "published before Run returned" from "published at all".
type kindLog struct {
	mu      sync.Mutex
	kinds   []event.Kind
	mark    int
	started chan struct{}
	arrived chan struct{}
}

func (l *kindLog) collect(facts <-chan event.Record, marker string) {
	var onceStarted, onceMarker sync.Once
	for rec := range facts {
		l.mu.Lock()
		l.kinds = append(l.kinds, rec.Event.Kind())
		l.mu.Unlock()
		switch v := rec.Event.(type) {
		case event.ToolCallStarted:
			onceStarted.Do(func() { close(l.started) })
		case event.Notice:
			if v.Text == marker {
				onceMarker.Do(func() {
					l.mu.Lock()
					l.mark = len(l.kinds) - 1
					l.mu.Unlock()
					close(l.arrived)
				})
			}
		}
	}
}

// index is where each kind first appeared, and where the marker did.
// A kind never seen is absent, not zero, which would read as "first".
func (l *kindLog) index() (map[event.Kind]int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	at := map[event.Kind]int{}
	for i, k := range l.kinds {
		if _, ok := at[k]; !ok {
			at[k] = i
		}
	}
	return at, l.mark
}

func waitFor(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	case <-time.After(10 * time.Second):
		return false
	}
}
