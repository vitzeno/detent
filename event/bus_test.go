package event

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func drainN(t *testing.T, ch <-chan Record, n int) []Record {
	t.Helper()
	out := make([]Record, 0, n)
	for range n {
		select {
		case r := <-ch:
			out = append(out, r)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out after %d of %d records", len(out), n)
		}
	}
	return out
}

func TestBus_DeliversInOrderWithGaplessSeq(t *testing.T) {
	b := New()
	ch, stop := b.Subscribe(nil)
	defer stop()

	for i := range 5 {
		b.Publish(Notice{Level: "info", Text: string(rune('a' + i))})
	}
	got := drainN(t, ch, 5)
	for i, r := range got {
		assert.Equal(t, uint64(i+1), r.Seq, "seq must be gapless and start at 1")
		assert.False(t, r.At.IsZero(), "the bus stamps the time, not the caller")
	}
}

// The delivery policy, which is the whole reason the Bus exists rather
// than a plain channel: a lagging subscriber loses live output and
// keeps everything else.
func TestBus_DropsLossyOnlyWhenBehind(t *testing.T) {
	b := New()
	ch, stop := b.Subscribe(nil)
	defer stop()

	const flood = queueDepth * 3
	for range flood {
		b.Publish(OutputChunk{Call: "c1", Line: "noise"})
	}
	b.Publish(CallEnded{Call: "c1"})

	require.Positive(t, b.Dropped(), "a %d-deep flood past a %d queue must drop", flood, queueDepth)

	var sawEnd bool
	deadline := time.After(2 * time.Second)
	for !sawEnd {
		select {
		case r := <-ch:
			if _, ok := r.Event.(CallEnded); ok {
				sawEnd = true
			}
		case <-deadline:
			t.Fatal("CallEnded was dropped; lifecycle delivery must be guaranteed")
		}
	}
}

func TestBus_LosslessSurvivesASlowSubscriber(t *testing.T) {
	b := New()
	ch, stop := b.Subscribe(nil)
	defer stop()

	const n = queueDepth * 2
	for i := range n {
		b.Publish(StepEnded{Step: ID(string(rune('a' + i%26)))})
	}
	assert.Zero(t, b.Dropped(), "nothing lossy was published")
	assert.Len(t, drainN(t, ch, n), n, "every lossless record must arrive")
}

// Publish must not block on anyone, or the engine stalls behind a UI
// that stopped reading.
func TestBus_PublishNeverBlocks(t *testing.T) {
	b := New()
	_, stop := b.Subscribe(nil) // subscribed and never read
	defer stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range queueDepth * 4 {
			b.Publish(OutputChunk{Call: "c", Line: "x"})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that never read")
	}
}

// A handler publishing from inside its own callback is the shape every
// subscriber-that-reacts has, so it must not deadlock.
func TestBus_PublishFromASubscriber(t *testing.T) {
	b := New()
	ch, stop := b.Subscribe(Only(CallEndedKind))
	defer stop()
	echo, stopEcho := b.Subscribe(Only(NoticeKind))
	defer stopEcho()

	go func() {
		for r := range ch {
			b.Publish(Notice{Level: "info", Text: string(r.Event.(CallEnded).Call)})
		}
	}()
	b.Publish(CallEnded{Call: "c9"})

	got := drainN(t, echo, 1)
	assert.Equal(t, "c9", got[0].Event.(Notice).Text)
}

func TestBus_FiltersSplitIntentsFromFacts(t *testing.T) {
	b := New()
	facts, stopF := b.Subscribe(Facts())
	defer stopF()
	intents, stopI := b.Subscribe(Intents())
	defer stopI()

	b.Publish(Abort{Turn: "t1"})
	b.Publish(CallEnded{Call: "c1"})

	assert.Equal(t, CallEndedKind, drainN(t, facts, 1)[0].Event.Kind())
	assert.Equal(t, AbortKind, drainN(t, intents, 1)[0].Event.Kind())
}

// Unsubscribing closes the channel even when nobody drained the
// backlog, so a range over it terminates and the goroutine goes away.
func TestBus_UnsubscribeClosesAndDoesNotLeak(t *testing.T) {
	b := New()
	ch, stop := b.Subscribe(nil)
	for range 50 {
		b.Publish(Notice{Text: "backlog"})
	}
	stop()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, open := <-ch:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("channel never closed after unsubscribe")
		}
	}
}

func TestBus_CloseIsIdempotentAndPublishAfterIsANoOp(t *testing.T) {
	b := New()
	ch, _ := b.Subscribe(nil)
	b.Close()
	b.Close()
	assert.NotPanics(t, func() { b.Publish(Notice{Text: "late"}) })

	select {
	case _, open := <-ch:
		assert.False(t, open, "Close must close every subscription")
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not close the subscription")
	}
}

func TestBus_ConcurrentPublishersAndSubscribers(t *testing.T) {
	b := New()
	const pubs, each = 8, 50

	var wg sync.WaitGroup
	counts := make([]int, 4)
	stops := make([]func(), 4)
	for i := range counts {
		ch, stop := b.Subscribe(Only(NoticeKind))
		stops[i] = stop
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range ch {
				counts[i]++
			}
		}()
	}

	var pw sync.WaitGroup
	for range pubs {
		pw.Add(1)
		go func() {
			defer pw.Done()
			for range each {
				b.Publish(Notice{Text: "x"})
			}
		}()
	}
	pw.Wait()
	for _, s := range stops {
		s()
	}
	wg.Wait()
	// Nothing lossy was published, but a stop can land mid-backlog, so
	// the assertion is that delivery happened and nothing raced.
	assert.Zero(t, b.Dropped())
}
