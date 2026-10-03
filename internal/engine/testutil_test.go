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
	err     error
	// stall, when set, blocks the next call until closed, ignoring ctx.
	stall chan struct{}
}

func (f *fakeModel) Complete(_ context.Context, msgs []event.Message, _ []map[string]any) (model.Reply, event.Usage, error) {
	f.mu.Lock()
	stall := f.stall
	f.stall = nil
	f.mu.Unlock()
	if stall != nil {
		<-stall
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, append([]event.Message(nil), msgs...))
	if f.err != nil {
		return model.Reply{}, event.Usage{}, f.err
	}
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
	// partial is what a held command printed before it was stopped.
	partial string
}

func (r *fakeRunner) Run(ctx context.Context, cmd string, lines chan<- capture.StreamEvent) (capture.Result, error) {
	r.mu.Lock()
	r.ran = append(r.ran, cmd)
	hold, out, pan, partial := r.hold, r.out, r.pan, r.partial
	r.mu.Unlock()

	if pan {
		panic("runner exploded")
	}
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return capture.Result{Stdout: partial}, ctx.Err()
		}
	}
	if lines != nil {
		for l := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
			lines <- capture.StreamEvent{Line: l}
		}
	}
	return capture.Result{Stdout: out}, nil
}

func (r *fakeRunner) commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ran...)
}

type fakeSelector struct{ r Runner }

// The sandbox, so every tool reaches the fake runner as a command. The host
// would run native tools in this process, which hostSelector is for.
func (f fakeSelector) Select(event.Risk) (Runner, string) { return f.r, "sandbox" }

type hostSelector struct{ r Runner }

func (h hostSelector) Select(event.Risk) (Runner, string) { return h.r, hostMode }

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
	return rigOn(t, bus, fm, runner, fakeSelector{runner}, reg, opts...)
}

// rigOn takes the selector too, for a test about where a tool call runs.
func rigOn(t *testing.T, bus *event.Bus, fm *fakeModel, runner Runner, sel RunnerSelector, reg *tool.Registry, opts ...Option) *rig {
	t.Helper()
	// The finishing check adds a Step to most Turns, so tests that are not
	// about it turn it off, and its own tests turn it back on.
	eng := New(bus, fm, reg, sel, append([]Option{WithFinishCheck(false)}, opts...)...)
	inner, _ := runner.(*fakeRunner)
	if s, ok := runner.(*snapRunner); ok {
		inner = s.fakeRunner
	}

	r := &rig{t: t, bus: bus, eng: eng, model: fm, runner: inner, subs: map[event.Kind][]chan event.Event{}}
	unsub := bus.Handle(event.Facts(), func(rec event.Record) { r.record(rec.Event) })

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
	ev, ok := r.waitNth(k, n)
	if !ok {
		r.t.Fatalf("timed out waiting for %s #%d; saw %v", k, n, r.kinds())
	}
	return ev
}

// waitNth is awaitNth for a goroutine other than the test's, which may
// not call Fatalf: it reports a timeout instead.
func (r *rig) waitNth(k event.Kind, n int) (event.Event, bool) {
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
				return ev, true
			}
		}
		ch := make(chan event.Event, 1)
		r.subs[k] = append(r.subs[k], ch)
		r.mu.Unlock()

		select {
		case <-ch:
		case <-deadline:
			return nil, false
		}
	}
}

// dispatched waits until the engine has handled every intent published so far.
// It reads them one at a time, so once it has taken one it ignores, the rest are done.
func (r *rig) dispatched() {
	r.bus.Publish(event.ListSessions{})
	r.bus.Settle(3 * time.Second)
}

// records is every fact so far as a store would hand them back.
func (r *rig) records() []event.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]event.Record, len(r.got))
	for i, ev := range r.got {
		out[i] = event.Record{Ordinal: uint64(i + 1), Event: ev}
	}
	return out
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

func bashCall(id, cmd string) event.ToolRequest {
	return event.ToolRequest{ID: id, Name: "bash", Args: map[string]any{"command": cmd}}
}

func readCall(id, path string) event.ToolRequest {
	return event.ToolRequest{ID: id, Name: "read_file", Args: map[string]any{"path": path}}
}

// answered checks every tool call of a finished engine was answered.
func answered(t *testing.T, e *Engine) {
	t.Helper()
	wellFormed(t, e.Transcript())
	for _, m := range e.Transcript() {
		if m.Role == event.RoleTool {
			require.NotEmpty(t, m.Content, "an answer may not be empty")
		}
	}
}

// compact drops whole Steps off the front until the transcript fits.
// Whole, because half a Step is a transcript no endpoint accepts.
func (t *transcript) compact(ctx context.Context, budgetTokens int, s Summarizer) (dropped int, note string) {
	cut := t.cutFor(budgetTokens)
	if cut == 0 {
		return 0, ""
	}
	note = summarise(ctx, s, t.msgs[:cut])
	t.fold(cut, note)
	return cut, note
}
