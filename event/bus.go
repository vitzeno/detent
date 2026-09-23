package event

import (
	"sync"
	"sync/atomic"
	"time"
)

// Bus fans Records out. Publish never blocks, so publishing from
// inside a handler is safe. Each subscriber has its own queue; a
// lagging one grows it and drops only Lossy events past queueDepth.
type Bus struct {
	mu      sync.Mutex
	subs    map[int]*sub
	next    int
	ordinal uint64
	// shut stops new publishes; closed tears the subscriptions down.
	// Two states, because Drain is the first without the second.
	shut    bool
	closed  bool
	dropped atomic.Uint64
}

func New() *Bus { return &Bus{subs: map[int]*sub{}} }

// Resume continues a stored session's ordinals, so a record published
// now cannot land on one already on disk. Call it before anything
// publishes; a replay never goes on the bus itself.
func (b *Bus) Resume(from uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if from > b.ordinal {
		b.ordinal = from
	}
}

// Publish stamps e and hands it to every interested subscriber.
func (b *Bus) Publish(e Event) {
	if e == nil {
		return
	}
	b.mu.Lock()
	if b.shut {
		b.mu.Unlock()
		return
	}
	b.ordinal++
	r := Record{Ordinal: b.ordinal, At: time.Now(), Event: e}
	targets := make([]*sub, 0, len(b.subs))
	for _, s := range b.subs {
		if s.filter == nil || s.filter(e) {
			targets = append(targets, s)
		}
	}
	b.mu.Unlock()

	for _, s := range targets {
		s.push(r)
	}
}

// Subscribe returns matching Records and a func that stops them. The
// channel closes on either, so a range over it terminates.
func (b *Bus) Subscribe(f Filter) (<-chan Record, func()) {
	s := newSub(f, &b.dropped)
	b.mu.Lock()
	if b.shut {
		b.mu.Unlock()
		close(s.out)
		return s.out, func() {}
	}
	id := b.next
	b.next++
	b.subs[id] = s
	b.mu.Unlock()

	go s.drain()
	return s.out, func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
		s.stop()
	}
}

// Drain stops accepting publishes, then waits up to timeout for every
// subscriber to receive what is already queued. Shutdown wants this
// and unsubscribing does not: one that has gone away is owed nothing.
func (b *Bus) Drain(timeout time.Duration) {
	b.mu.Lock()
	b.shut = true
	b.mu.Unlock()
	b.Settle(timeout)
	b.Close()
}

// Settle waits up to timeout for every subscriber to receive what is
// queued, without shutting anything, so it orders one thing after
// another rather than only ending a session.
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

// Close stops every subscription; publishing afterwards is a no-op.
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

// Dropped counts lossy Records nobody received. Survives Close,
// because the number is only useful afterwards.
func (b *Bus) Dropped() uint64 { return b.dropped.Load() }

// Filter reports whether a subscriber wants an Event. nil takes all.
type Filter func(Event) bool

// Only takes the named kinds.
func Only(kinds ...Kind) Filter {
	want := make(map[Kind]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}
	return func(e Event) bool { return want[e.Kind()] }
}

// Intents is what the engine listens to, Facts what a front-end does.
func Intents() Filter { return func(e Event) bool { return e.Kind().IsIntent() } }
func Facts() Filter   { return func(e Event) bool { return !e.Kind().IsIntent() } }

// queueDepth: past this, a lagging subscriber drops lossy events.
const queueDepth = 512

type sub struct {
	filter  Filter
	out     chan Record
	done    chan struct{}
	dropped *atomic.Uint64

	mu    sync.Mutex
	cond  *sync.Cond
	queue []Record
	// pushed and taken bracket delivery: a Record off the queue but
	// not yet received is still owed, which is what Drain waits on.
	pushed, taken uint64
	closed        bool
}

func newSub(f Filter, dropped *atomic.Uint64) *sub {
	s := &sub{filter: f, out: make(chan Record, 1), done: make(chan struct{}), dropped: dropped}
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
	defer close(s.out)
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
		s.queue = s.queue[1:]
		s.mu.Unlock()

		select {
		case s.out <- r:
			s.mu.Lock()
			s.taken++
			s.mu.Unlock()
		case <-s.done:
			return
		}
	}
}

// owed is how much has been pushed but not yet received.
func (s *sub) owed() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pushed - s.taken
}

func (s *sub) stop() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	close(s.done)
	s.cond.Broadcast()
}
