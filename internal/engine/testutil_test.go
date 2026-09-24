package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// fakeModel replies with a scripted Step per call, then stops.
type fakeModel struct {
	mu      sync.Mutex
	replies []model.Reply
	seen    [][]event.Message
}

func (f *fakeModel) Complete(_ context.Context, msgs []event.Message, _ []map[string]any) (model.Reply, event.Usage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, append([]event.Message(nil), msgs...))
	if len(f.replies) == 0 {
		return model.Reply{Text: "finished", Stop: "stop"}, event.Usage{PromptTokens: 1}, nil
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	return r, event.Usage{PromptTokens: 1, CompletionTokens: 1}, nil
}

func (f *fakeModel) lastSent() []event.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) == 0 {
		return nil
	}
	return f.seen[len(f.seen)-1]
}

// fakeRunner records commands and replies with canned output.
type fakeRunner struct {
	mu   sync.Mutex
	ran  []string
	out  string
	hold chan struct{} // when set, Run blocks until closed
	pan  bool
}

func (r *fakeRunner) Run(ctx context.Context, cmd string, lines chan<- capture.StreamEvent) (capture.Result, error) {
	r.mu.Lock()
	r.ran = append(r.ran, cmd)
	hold, out, pan := r.hold, r.out, r.pan
	r.mu.Unlock()

	if pan {
		panic("runner exploded")
	}
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			if lines != nil {
				close(lines)
			}
			return capture.Result{}, ctx.Err()
		}
	}
	if lines != nil {
		for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			lines <- capture.StreamEvent{Line: l}
		}
		close(lines)
	}
	return capture.Result{Stdout: out}, nil
}

func (r *fakeRunner) commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ran...)
}

type fakeSelector struct{ r Runner }

func (f fakeSelector) Select(event.Risk) (Runner, string) { return f.r, "host" }

// snapRunner adds Snapshotter, which the engine finds by type assertion.
type snapRunner struct {
	*fakeRunner
	mu       sync.Mutex
	taken    []string
	restored []string
}

func (s *snapRunner) Snapshot(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := "snap-" + string(rune('a'+len(s.taken)))
	s.taken = append(s.taken, id)
	return id, nil
}

func (s *snapRunner) Rollback(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restored = append(s.restored, id)
	return nil
}

// rig is one engine wired to a bus, running, with its facts collected.
type rig struct {
	t      *testing.T
	bus    *event.Bus
	eng    *Engine
	model  *fakeModel
	runner *fakeRunner

	mu   sync.Mutex
	got  []event.Event
	subs map[event.Kind][]chan event.Event
}

func newRig(t *testing.T, replies []model.Reply, opts ...Option) *rig {
	t.Helper()
	runner := &fakeRunner{out: "output\n"}
	return rigWith(t, event.New(), &fakeModel{replies: replies}, runner, opts...)
}

// rigWith takes the runner, so a test can supply one that snapshots.
func rigWith(t *testing.T, bus *event.Bus, fm *fakeModel, runner Runner, opts ...Option) *rig {
	t.Helper()
	return rigWithTools(t, bus, fm, runner, tool.Standard(), opts...)
}

// rigWithTools takes the registry, so a test can add a tool the
// shipped set does not have.
func rigWithTools(t *testing.T, bus *event.Bus, fm *fakeModel, runner Runner, reg *tool.Registry, opts ...Option) *rig {
	t.Helper()
	eng := New(bus, fm, reg, fakeSelector{runner}, opts...)
	inner, _ := runner.(*fakeRunner)
	if s, ok := runner.(*snapRunner); ok {
		inner = s.fakeRunner
	}

	r := &rig{t: t, bus: bus, eng: eng, model: fm, runner: inner, subs: map[event.Kind][]chan event.Event{}}
	facts, unsub := bus.Subscribe(event.Facts())
	go func() {
		for rec := range facts {
			r.record(rec.Event)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	go eng.Run(ctx)
	t.Cleanup(func() { cancel(); unsub(); bus.Close() })

	// Run subscribes to intents, then publishes this. Waiting on it is
	// what stops a test publishing before anyone is listening.
	r.await(event.SessionStartedKind)
	return r
}

func (r *rig) record(ev event.Event) {
	r.mu.Lock()
	r.got = append(r.got, ev)
	waiting := r.subs[ev.Kind()]
	r.subs[ev.Kind()] = nil
	r.mu.Unlock()
	for _, ch := range waiting {
		ch <- ev
	}
}

// await blocks for the first event of a kind, already seen or not.
func (r *rig) await(k event.Kind) event.Event { return r.awaitNth(k, 1) }

// awaitNth blocks for the nth event of a kind. Counting matters: a
// second Turn's end must not be satisfied by the first one's.
func (r *rig) awaitNth(k event.Kind, n int) event.Event {
	r.t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		r.mu.Lock()
		var seen int
		for _, ev := range r.got {
			if ev.Kind() != k {
				continue
			}
			if seen++; seen == n {
				r.mu.Unlock()
				return ev
			}
		}
		ch := make(chan event.Event, 1)
		r.subs[k] = append(r.subs[k], ch)
		r.mu.Unlock()

		select {
		case <-ch:
		case <-deadline:
			r.t.Fatalf("timed out waiting for %s #%d; saw %v", k, n, r.kinds())
			return nil
		}
	}
}

func (r *rig) kinds() []event.Kind {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]event.Kind, 0, len(r.got))
	for _, e := range r.got {
		out = append(out, e.Kind())
	}
	return out
}

func (r *rig) of(k event.Kind) []event.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []event.Event
	for _, e := range r.got {
		if e.Kind() == k {
			out = append(out, e)
		}
	}
	return out
}

// run submits a prompt and waits for this Turn to end, not an
// earlier one.
func (r *rig) run(prompt string) event.TurnEnded {
	r.t.Helper()
	n := len(r.of(event.TurnEndedKind)) + 1
	r.bus.Publish(event.SubmitPrompt{Text: prompt})
	return r.awaitNth(event.TurnEndedKind, n).(event.TurnEnded)
}

func bashCall(id, cmd string) event.ToolCall {
	return event.ToolCall{ID: id, Name: "bash", Args: map[string]any{"command": cmd}}
}

func readCall(id, path string) event.ToolCall {
	return event.ToolCall{ID: id, Name: "read_file", Args: map[string]any{"path": path}}
}

// answered checks the phase gate on a finished engine.
func answered(t *testing.T, e *Engine) {
	t.Helper()
	wellFormed(t, e.Transcript())
	for _, m := range e.Transcript() {
		if m.Role == event.RoleTool {
			require.NotEmpty(t, m.Content, "an answer may not be empty")
		}
	}
}
