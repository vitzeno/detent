package event

import (
	"sync"
	"sync/atomic"
	"time"
)

// Bus fans Records out to subscribers. Publish never blocks, whoever
// is listening and however slowly, because the engine must not be
// stalled by a lagging UI — so publishing from inside a handler is
// safe and cannot deadlock.
//
// Each subscriber has its own queue. A lagging one grows it, except
// for events that say they are Lossy, which are dropped past a depth.
// That is the whole delivery policy: live output may be lost, nothing
// else may.
type Bus struct {
	mu      sync.Mutex
	subs    map[int]*sub
	next    int
	seq     uint64
	shut    bool
	dropped atomic.Uint64
}

func New() *Bus { return &Bus{subs: map[int]*sub{}} }

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
	b.seq++
	r := Record{Seq: b.seq, At: time.Now(), Event: e}
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

// Subscribe returns a channel of matching Records and a func that
// stops it. The channel closes once that func is called, or the Bus
// shuts down, so a range over it terminates.
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

// Close stops every subscription. Publishing afterwards is a no-op
// rather than a panic: shutdown races are not worth crashing over.
func (b *Bus) Close() {
	b.mu.Lock()
	if b.shut {
		b.mu.Unlock()
		return
	}
	b.shut = true
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

// Dropped is how many lossy Records never reached a subscriber. It
// survives unsubscribe and Close, because the number is only useful
// after the fact: non-zero means something could not keep up with live
// output, which is worth knowing and not worth failing over.
func (b *Bus) Dropped() uint64 { return b.dropped.Load() }

// Filter reports whether a subscriber wants an Event. nil takes all.
type Filter func(Event) bool

// Only takes the named kinds and nothing else.
func Only(kinds ...Kind) Filter {
	want := make(map[Kind]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}
	return func(e Event) bool { return want[e.Kind()] }
}

// Intents is what the engine listens to; Facts is what a front-end does.
func Intents() Filter { return func(e Event) bool { return e.Kind().IsIntent() } }
func Facts() Filter   { return func(e Event) bool { return !e.Kind().IsIntent() } }

// queueDepth is where a lagging subscriber starts dropping lossy
// events. Deep enough that an ordinary redraw pause loses nothing,
// shallow enough that a wedged consumer cannot eat memory unbounded.
const queueDepth = 512

type sub struct {
	filter  Filter
	out     chan Record
	done    chan struct{}
	dropped *atomic.Uint64

	mu     sync.Mutex
	cond   *sync.Cond
	queue  []Record
	closed bool
}

func newSub(f Filter, dropped *atomic.Uint64) *sub {
	s := &sub{filter: f, out: make(chan Record, 1), done: make(chan struct{}), dropped: dropped}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// push is the non-blocking half. A full queue drops a lossy Record and
// keeps everything else, which is what makes lifecycle delivery
// guaranteed and live output best effort.
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
	s.cond.Signal()
}

// drain is the blocking half, and the only thing that waits on the
// consumer. It abandons the backlog on stop: a subscriber that has
// unsubscribed is not owed the rest, and delivering it is how the
// goroutine leaks when nobody is reading any more.
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
		case <-s.done:
			return
		}
	}
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
