package event

import (
	"sync"
	"sync/atomic"
	"time"
)

// queueDepth: past this, a lagging subscriber drops lossy events.
const queueDepth = 512

// Bus fans Records out. Publish never blocks, so publishing from
// inside a handler is safe. Each subscriber has its own queue, and a
// lagging one grows it and drops only Lossy events past queueDepth.
type Bus struct {
	mu      sync.Mutex
	subs    map[int]*sub
	next    int
	ordinal uint64
	// shut stops new publishes, closed tears the subscriptions down.
	// Two states, because Drain is the first without the second.
	shut    bool
	closed  bool
	dropped atomic.Uint64
}

// New returns an open Bus with no subscribers.
func New() *Bus { return &Bus{subs: map[int]*sub{}} }

// Resume continues a stored session's ordinals, so a new record cannot
// land on one already on disk. Call it before anything publishes.
func (b *Bus) Resume(from uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if from > b.ordinal {
		b.ordinal = from
	}
}

// Publish stamps e and hands it to every interested subscriber. The
// pushes happen under the lock, so every subscriber sees ordinal order.
func (b *Bus) Publish(e Event) {
	if e == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.shut {
		return
	}
	b.ordinal++
	r := Record{Ordinal: b.ordinal, At: time.Now(), Event: e}
	for _, s := range b.subs {
		if wants(s.filter, e) {
			s.push(r)
		}
	}
}

// Subscribe returns matching Records and a func that stops them. The
// channel closes on either, so a range over it terminates.
func (b *Bus) Subscribe(f Filter) (<-chan Record, func()) {
	out := make(chan Record)
	s := newSub(f, &b.dropped, func(r Record, done <-chan struct{}) bool {
		select {
		case out <- r:
			return true
		case <-done:
			return false
		}
	})
	if !b.add(s) {
		close(out)
		return out, func() {}
	}
	go func() {
		defer close(out)
		s.drain()
	}()
	return out, func() {
		b.remove(s)
		s.stop()
	}
}

// Handle calls h with each matching Record in turn, and Settle waits for h
// to return. Its stop waits for a running h, so h must never call it.
func (b *Bus) Handle(f Filter, h func(Record)) func() {
	s := newSub(f, &b.dropped, func(r Record, _ <-chan struct{}) bool {
		h(r)
		return true
	})
	if !b.add(s) {
		return func() {}
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		s.drain()
	}()
	return func() {
		b.remove(s)
		s.stop()
		<-finished
	}
}

// Drain stops accepting publishes, then waits up to timeout for every
// subscriber to finish with what is already queued, then closes.
func (b *Bus) Drain(timeout time.Duration) {
	b.mu.Lock()
	b.shut = true
	b.mu.Unlock()
	b.Settle(timeout)
	b.Close()
}

// Settle waits up to timeout for every subscriber to finish what is queued:
// received for a Subscribe channel, returned from for a Handle func.
func (b *Bus) Settle(timeout time.Duration) {
	b.mu.Lock()
	subs := make([]*sub, 0, len(b.subs))
	for _, s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.Unlock()

	deadline := time.Now().Add(timeout)
	for _, s := range subs {
		for s.owed() > 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}
}

// Close stops every subscription. Publishing afterwards is a no-op.
func (b *Bus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.shut, b.closed = true, true
	subs := make([]*sub, 0, len(b.subs))
	for _, s := range b.subs {
		subs = append(subs, s)
	}
	b.subs = map[int]*sub{}
	b.mu.Unlock()

	for _, s := range subs {
		s.stop()
	}
}

// Dropped counts lossy deliveries dropped, summed over subscribers.
// Survives Close, because the number is only useful afterwards.
func (b *Bus) Dropped() uint64 { return b.dropped.Load() }

// Filter reports whether a subscriber wants an Event. nil takes all. It
// runs under the bus lock, so it must be fast and never publish.
type Filter func(Event) bool

// Only takes the named kinds.
func Only(kinds ...Kind) Filter {
	want := make(map[Kind]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}
	return func(e Event) bool { return want[e.Kind()] }
}

// Intents takes what someone wants, which the engine and other owners listen to.
func Intents() Filter { return func(e Event) bool { return e.Kind().IsIntent() } }

// Facts takes what happened, which a front-end listens to.
func Facts() Filter { return func(e Event) bool { return !e.Kind().IsIntent() } }

// wants runs a filter, and treats one that panics as not interested
// rather than letting it unwind through Publish.
func wants(f Filter, e Event) (ok bool) {
	if f == nil {
		return true
	}
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return f(e)
}

// add registers s, or reports that the bus no longer takes subscribers.
func (b *Bus) add(s *sub) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.shut {
		return false
	}
	s.id = b.next
	b.next++
	b.subs[s.id] = s
	return true
}

func (b *Bus) remove(s *sub) {
	b.mu.Lock()
	delete(b.subs, s.id)
	b.mu.Unlock()
}

type sub struct {
	id      int
	filter  Filter
	deliver func(r Record, done <-chan struct{}) bool
	done    chan struct{}
	dropped *atomic.Uint64

	mu    sync.Mutex
	cond  *sync.Cond
	queue []Record
	// pushed and taken bracket delivery: a Record off the queue but
	// not yet delivered is still owed, which is what Drain waits on.
	pushed, taken uint64
	closed        bool
}

func newSub(f Filter, dropped *atomic.Uint64, deliver func(Record, <-chan struct{}) bool) *sub {
	s := &sub{filter: f, deliver: deliver, done: make(chan struct{}), dropped: dropped}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// push is the non-blocking half: a full queue drops lossy, keeps the rest.
func (s *sub) push(r Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if len(s.queue) >= queueDepth && r.Event.Lossy() {
		s.dropped.Add(1)
		return
	}
	s.queue = append(s.queue, r)
	s.pushed++
	s.cond.Signal()
}

// drain is the only thing that waits on the consumer. It abandons the
// backlog on stop, which is what stops the goroutine leaking.
func (s *sub) drain() {
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.closed {
			s.cond.Wait()
		}
		if s.closed {
			s.mu.Unlock()
			return
		}
		r := s.queue[0]
		// Cleared so a delivered burst is not kept alive by the backing array.
		s.queue[0] = Record{}
		s.queue = s.queue[1:]
		if len(s.queue) == 0 {
			s.queue = nil
		}
		s.mu.Unlock()

		if !s.deliver(r, s.done) {
			return
		}
		s.mu.Lock()
		s.taken++
		s.mu.Unlock()
	}
}

// owed is how much has been pushed but not yet delivered. A stopped
// subscriber owes nothing, since nobody will take its backlog.
func (s *sub) owed() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0
	}
	return s.pushed - s.taken
}

func (s *sub) stop() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.queue = nil
	s.mu.Unlock()
	close(s.done)
	s.cond.Broadcast()
}
