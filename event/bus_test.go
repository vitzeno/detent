package event

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBus_DeliversInOrderWithGaplessOrdinals(t *testing.T) {
	b := New()
	ch, stop := b.Subscribe(nil)
	defer stop()

	for i := range 5 {
		b.Publish(Notice{Level: "info", Text: string(rune('a' + i))})
	}
	got := drainN(t, ch, 5)
	for i, r := range got {
		assert.Equal(t, uint64(i+1), r.Ordinal, "ordinals must be gapless and start at 1")
		assert.False(t, r.At.IsZero(), "the bus stamps the time, not the caller")
	}
}

// The reason the Bus is not a plain channel: a lagging subscriber loses
// live output and keeps everything else.
func TestBus_DropsLossyOnlyWhenBehind(t *testing.T) {
	b := New()
	ch, stop := b.Subscribe(nil)
	defer stop()

	const flood = queueDepth * 3
	for range flood {
		b.Publish(OutputChunk{Call: testID("c1"), Line: "noise"})
	}
	b.Publish(CallEnded{Call: testID("c1")})

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
		b.Publish(StepEnded{Step: testID(string(rune('a' + i%26)))})
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
			b.Publish(OutputChunk{Call: testID("c"), Line: "x"})
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
			b.Publish(Notice{Level: "info", Text: r.Event.(CallEnded).Call.String()})
		}
	}()
	b.Publish(CallEnded{Call: testID("c9")})

	got := drainN(t, echo, 1)
	assert.Equal(t, testID("c9").String(), got[0].Event.(Notice).Text)
}

func TestBus_FiltersSplitIntentsFromFacts(t *testing.T) {
	b := New()
	facts, stopF := b.Subscribe(Facts())
	defer stopF()
	intents, stopI := b.Subscribe(Intents())
	defer stopI()

	b.Publish(Abort{Turn: testID("t1")})
	b.Publish(CallEnded{Call: testID("c1")})

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
		wg.Go(func() {
			for range ch {
				counts[i]++
			}
		})
	}

	var pw sync.WaitGroup
	for range pubs {
		pw.Go(func() {
			for range each {
				b.Publish(Notice{Text: "x"})
			}
		})
	}
	pw.Wait()
	b.Settle(5 * time.Second)
	for _, s := range stops {
		s()
	}
	wg.Wait()
	assert.Zero(t, b.Dropped())
	for i, n := range counts {
		assert.Equal(t, pubs*each, n, "subscriber %d missed records", i)
	}
}

// Ordinals are minted in one order, so every subscriber must see that
// order, or replay and the live view disagree about what came first.
func TestBus_ConcurrentPublishersDeliverInOrdinalOrder(t *testing.T) {
	b := New()
	const pubs, each = 8, 200
	subs := make([]<-chan Record, 3)
	for i := range subs {
		ch, stop := b.Subscribe(nil)
		defer stop()
		subs[i] = ch
	}

	var pw sync.WaitGroup
	for range pubs {
		pw.Go(func() {
			for range each {
				b.Publish(Notice{Text: "x"})
			}
		})
	}
	for i, ch := range subs {
		got := drainN(t, ch, pubs*each)
		for j := 1; j < len(got); j++ {
			require.Less(t, got[j-1].Ordinal, got[j].Ordinal, "subscriber %d saw ordinals out of order", i)
		}
	}
	pw.Wait()
}

// A filter that panics must cost only its own subscriber, not leave the
// bus locked for everyone.
func TestBus_APanickingFilterIsNotInterested(t *testing.T) {
	b := New()
	defer b.Close()
	_, stopBad := b.Subscribe(func(Event) bool { panic("filter") })
	defer stopBad()
	ch, stop := b.Subscribe(nil)
	defer stop()

	assert.NotPanics(t, func() { b.Publish(Notice{Text: "x"}) })
	assert.NotPanics(t, func() { b.Publish(Notice{Text: "y"}) })
	assert.Len(t, drainN(t, ch, 2), 2, "the bus still delivers to everyone else")
}

// Settle on a Handle subscriber means handled, which is what a test
// ordering one thing after a subscriber's reaction needs.
func TestBus_SettleWaitsForAHandlerToReturn(t *testing.T) {
	b := New()
	defer b.Close()
	var handled atomic.Int64
	stop := b.Handle(nil, func(Record) {
		time.Sleep(time.Millisecond)
		handled.Add(1)
	})
	defer stop()

	for range 20 {
		b.Publish(Notice{Text: "x"})
	}
	b.Settle(3 * time.Second)
	assert.EqualValues(t, 20, handled.Load())
}

// Stop must not return while a handler is still running, or it acts on
// things its owner has already closed.
func TestBus_HandleStopWaitsForTheRunningHandler(t *testing.T) {
	b := New()
	defer b.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	var finished atomic.Bool
	stop := b.Handle(nil, func(Record) {
		close(entered)
		<-release
		finished.Store(true)
	})
	b.Publish(Notice{Text: "x"})
	<-entered

	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("stop returned while the handler was running")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-stopped
	assert.True(t, finished.Load())
}

// A subscriber stopped with a backlog owes nothing, so Settle does not
// wait out its whole timeout on it.
func TestBus_SettleSkipsAStoppedBacklog(t *testing.T) {
	b := New()
	defer b.Close()
	_, stop := b.Subscribe(nil)
	for range 50 {
		b.Publish(Notice{Text: "x"})
	}
	stop()
	start := time.Now()
	b.Settle(2 * time.Second)
	assert.Less(t, time.Since(start), time.Second)
}

func TestBus_SubscribeAfterDrainIsClosed(t *testing.T) {
	b := New()
	b.Drain(time.Second)
	ch, stop := b.Subscribe(nil)
	defer stop()
	_, open := <-ch
	assert.False(t, open)
	assert.NotPanics(t, b.Handle(nil, func(Record) {}))
}

// Shutdown is the one time a backlog must not be abandoned: the
// consumer is still reading and is owed the rest.
func TestBus_DrainDeliversTheBacklogBeforeClosing(t *testing.T) {
	b := New()
	ch, _ := b.Subscribe(nil)

	const n = 200
	for i := range n {
		b.Publish(StepEnded{Step: testID(string(rune('a' + i%26)))})
	}

	var got int
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range ch {
			got++
			time.Sleep(time.Microsecond) // a consumer that is not instant
		}
	}()

	b.Drain(5 * time.Second)
	<-done
	assert.Equal(t, n, got, "everything published before Drain must arrive")
}

func TestBus_DrainGivesUpRatherThanHanging(t *testing.T) {
	b := New()
	_, _ = b.Subscribe(nil) // subscribed and never read
	for range 100 {
		b.Publish(Notice{Text: "x"})
	}
	start := time.Now()
	b.Drain(100 * time.Millisecond)
	assert.Less(t, time.Since(start), 3*time.Second, "a wedged consumer must not hold up exit")
}

// A resumed session mints ordinals in a fresh process, so without
// continuing from the stored ones it would overwrite them.
func TestBus_ResumeContinuesTheOrdinals(t *testing.T) {
	b := New()
	b.Resume(41)
	ch, stop := b.Subscribe(nil)
	defer stop()

	b.Publish(Notice{Text: "after a restart"})
	assert.EqualValues(t, 42, drainN(t, ch, 1)[0].Ordinal)

	b.Resume(7)
	b.Publish(Notice{Text: "cannot go backwards"})
	assert.EqualValues(t, 43, drainN(t, ch, 1)[0].Ordinal)
}

// Settle is Drain without the shutdown: useful for ordering one thing
// after another, where Drain would stop the bus for good.
func TestBus_SettleDeliversThenCarriesOn(t *testing.T) {
	b := New()
	defer b.Close()
	ch, stop := b.Subscribe(nil)
	defer stop()

	var got atomic.Int64
	go func() {
		for range ch {
			got.Add(1)
		}
	}()

	for range 50 {
		b.Publish(Notice{Text: "before"})
	}
	b.Settle(3 * time.Second)
	assert.EqualValues(t, 50, got.Load(), "everything published so far has been received")

	b.Publish(Notice{Text: "after"})
	b.Settle(3 * time.Second)
	assert.EqualValues(t, 51, got.Load(), "and the bus still works")
}

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

// testID makes a deterministic uuid from a readable name, so a test
// can still say "c1" and mean it.
func testID(name string) uuid.UUID { return uuid.NewSHA1(uuid.Nil, []byte(name)) }
