package agent

import (
	"context"
	"sync"
	"time"

	"github.com/vitzeno/detent/internal/probe"
	"github.com/vitzeno/detent/logging"
)

// Environment probes are gathered while nobody is waiting: once when
// the session starts, and again as each goal closes. A goal that then
// arrives reads what is already there instead of waiting for commands
// to run.
//
// After a goal rather than before the next one, which sounds like the
// same idle time and is not. Capturing before a goal means capturing,
// then waiting however long a human takes to type, then presenting it
// as "gathered before this goal". Capturing as a goal closes is
// capturing the thing that just changed, at the moment nothing else
// will change it.

// probeCache holds the last gathered environment. The zero value is a
// miss, which is the honest answer before anything has been gathered.
type probeCache struct {
	mu    sync.Mutex
	ready chan struct{} // closed when a gather finishes; nil when none is running
	out   map[string]string
	at    time.Time
}

// staleAfter is how old a gather may be and still be used. Nothing
// detent ran can have changed the environment since the last gather,
// but something else on the machine can, so it is a ceiling rather
// than a guarantee.
const staleAfter = 5 * time.Minute

// Prime gathers the environment before there is a goal, so the first
// one reads it rather than waiting. A caller runs this once the
// session is wired; it is not done in New because starting commands
// as a side effect of construction is a surprise, and every test that
// builds a Session would pay for it.
func (s *Session) Prime(ctx context.Context) { s.gather(ctx) }

// gather runs every probe in the menu and keeps what each said. It is
// the whole menu and not a chosen subset because choosing needs a
// goal, and the point is to have run before there is one.
func (s *Session) gather(ctx context.Context) {
	if s.Runners == nil {
		return
	}
	s.probes.mu.Lock()
	if s.probes.ready != nil {
		s.probes.mu.Unlock()
		return // one in flight is enough
	}
	done := make(chan struct{})
	s.probes.ready = done
	s.probes.mu.Unlock()

	go func() {
		t0 := time.Now()
		out := probe.New(probeRunner{runner: s.Runners.Probe()}).Each(ctx, probe.Menu)

		s.probes.mu.Lock()
		s.probes.out, s.probes.at, s.probes.ready = out, time.Now(), nil
		s.probes.mu.Unlock()
		close(done)

		logging.For(logging.Agent).DebugContext(ctx, "environment gathered",
			logging.KeyEvent, logging.ProbeRun, "probes", len(out), logging.KeyMS, ms(time.Since(t0)))
	}()
}

// take returns the gathered output for the named probes, waiting for
// a gather already in flight. ok is false when there is nothing usable,
// and the caller runs them itself rather than going without.
func (s *Session) take(ctx context.Context, names []probe.Probe) (map[string]string, bool) {
	s.probes.mu.Lock()
	inFlight := s.probes.ready
	s.probes.mu.Unlock()

	if inFlight != nil {
		select {
		case <-inFlight:
		case <-ctx.Done():
			return nil, false
		}
	}

	s.probes.mu.Lock()
	defer s.probes.mu.Unlock()
	if s.probes.out == nil || time.Since(s.probes.at) > staleAfter {
		return nil, false
	}
	for _, p := range names {
		if _, ok := s.probes.out[p.Name]; !ok {
			return nil, false // gathered before this probe existed
		}
	}
	return s.probes.out, true
}
