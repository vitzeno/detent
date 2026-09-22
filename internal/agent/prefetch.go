package agent

import (
	"context"
	"sync"
	"time"

	"github.com/vitzeno/detent/internal/probe"
	"github.com/vitzeno/detent/logging"
)

// Prime gathers the environment before there is a goal, so the first
// one reads it rather than waiting. Not done in New: starting commands
// as a side effect of construction is a surprise.
func (s *Session) Prime(ctx context.Context) { s.gather(ctx) }

// take returns gathered output for the named probes, waiting on a
// gather already in flight. ok is false when there is nothing usable,
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

// gather runs the whole menu and keeps what each probe said. The whole
// menu because choosing needs a goal, and the point is to have run
// before there is one.
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

// probeCache holds the last gathered environment, filled as each goal
// closes rather than before the next one starts: gathering before a
// goal presents something captured an unbounded wait ago as current.
type probeCache struct {
	mu    sync.Mutex
	ready chan struct{} // closed when a gather finishes; nil when none is running
	out   map[string]string
	at    time.Time
}

// staleAfter is a ceiling, not a guarantee: nothing detent ran can
// have changed the machine since the last gather, but something else
// on it can.
const staleAfter = 5 * time.Minute
