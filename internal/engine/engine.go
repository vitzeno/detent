// Package engine owns the agent loop, and drives itself. That is what
// lets front-ends subscribe rather than call.
package engine

import (
	"context"
	"github.com/google/uuid"
	"sync"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// Defaults. MaxSteps is generous and soft: hitting it asks rather than
// stops, because a human is watching.
const (
	DefaultMaxSteps      = 50
	DefaultCallsPerStep  = 10
	DefaultParallelCalls = 4
	DefaultRepeatLimit   = 3
)

// Engine is one session. Run it once, in its own goroutine; everything
// else reaches it through the bus.
type Engine struct {
	bus     *event.Bus
	model   Completer
	tools   *tool.Registry
	runners RunnerSelector

	assessors []Assessor
	invoker   Invoker
	repeat    *repeatHook

	session uuid.UUID
	tr      transcript
	turns   int

	maxSteps      int
	maxCalls      int
	parallel      int
	contextTokens int
	summarizer    Summarizer

	worktreer Worktreer

	// How this run describes itself, for the fact published at start.
	modelName string
	judgeName string
	network   bool
	recorded  bool
	resumed   int

	// intents is subscribed in New, not Run: a caller that publishes
	// the moment New returns must not lose it to a goroutine that has
	// not started yet.
	intents <-chan event.Record
	unsub   func()

	// trMu guards the transcript, which the Turn goroutine writes and
	// a caller may read at any moment.
	trMu sync.Mutex

	mu sync.Mutex
	// cur is the Turn in flight; past is what can still be undone.
	cur  *turnState
	past map[uuid.UUID]*turnState
}

// Completer is one model round trip: a Step.
type Completer interface {
	Complete(ctx context.Context, msgs []event.Message, tools []map[string]any) (model.Reply, event.Usage, error)
}

// Runner executes one command. host.Shell and sandbox.Container both
// satisfy it structurally, so neither imports this package.
type Runner interface {
	Run(ctx context.Context, command string, events chan<- capture.StreamEvent) (capture.Result, error)
}

// Invoker answers a Call that has no command to run. internal/mcp
// satisfies it, so the engine never imports a client.
type Invoker interface {
	Invoke(ctx context.Context, c tool.Call) capture.Result
}

// RunnerSelector picks host or sandbox per Call.
type RunnerSelector interface {
	Select(r event.Risk) (Runner, string)
}

// Snapshotter is an optional Runner capability, found by type
// assertion. Without one there is nothing to roll back to.
type Snapshotter interface {
	Snapshot(ctx context.Context) (string, error)
	Rollback(ctx context.Context, id string) error
}

// Worktreer checkpoints the human's own files, which the container
// snapshot never covers.
type Worktreer interface {
	Checkpoint(ctx context.Context) string
	Restore(ctx context.Context, id string) error
}

func New(bus *event.Bus, m Completer, tools *tool.Registry, runners RunnerSelector, opts ...Option) *Engine {
	e := &Engine{
		bus: bus, model: m, tools: tools, runners: runners,
		session:       uuid.Must(uuid.NewV7()),
		maxSteps:      DefaultMaxSteps,
		maxCalls:      DefaultCallsPerStep,
		parallel:      DefaultParallelCalls,
		contextTokens: DefaultContextTokens,
		repeat:        newRepeatHook(DefaultRepeatLimit),
		past:          map[uuid.UUID]*turnState{},
	}
	for _, o := range opts {
		o(e)
	}
	// Cheapest first, the network hook last.
	e.assessors = append([]Assessor{toolFloor{}, mcpFloor{}, regexHook{}, e.repeat}, e.assessors...)
	e.intents, e.unsub = bus.Subscribe(event.Intents())
	return e
}

// Run is the actor loop. It reads intents and nothing else; every fact
// it produces goes out on the bus.
func (e *Engine) Run(ctx context.Context) {
	defer e.unsub()
	_, mode := e.runners.Select(event.UnknownRisk())
	e.bus.Publish(event.SessionStarted{
		Session: e.session, Model: e.modelName, Judge: e.judgeName,
		Sandbox: mode == "sandbox",
		Network: e.network, MaxSteps: e.maxSteps,
		Recorded: e.recorded, Resumed: e.resumed,
		ContextTokens: e.budget(),
	})
	done := make(chan struct{}, 1)

	for {
		select {
		case <-ctx.Done():
			e.abortCurrent()
			return
		case <-done:
			e.finishTurn()
		case rec, ok := <-e.intents:
			if !ok {
				return
			}
			e.dispatch(ctx, rec.Event, done)
		}
	}
}

// dispatch routes one intent. A running Turn owns the transcript, so
// only an idle engine touches it here.
func (e *Engine) dispatch(ctx context.Context, ev event.Event, done chan struct{}) {
	t := e.current()
	switch v := ev.(type) {
	case event.SubmitPrompt:
		if t != nil {
			// Typed mid-Turn, a prompt is steering, not a new request.
			t.post(event.NoteContext{Text: v.Text})
			return
		}
		e.startTurn(ctx, v.Text, done)
	case event.ResetSession:
		if t != nil {
			t.post(event.Abort{Turn: t.id})
			return
		}
		e.trLock(func() { e.tr.reset() })
		e.repeat.forget()
		e.turns = 0
		e.notice("info", "session reset")
	case event.RequestRollback:
		if t != nil {
			e.notice("warn", "cannot roll back while a request is running")
			return
		}
		e.rollback(ctx, v)
	case event.Abort:
		// Not queued: a blocked Call never reaches a boundary, and
		// the inbox is only drained at one.
		if t != nil {
			t.absorb(v)
		}
	default:
		if t != nil {
			t.post(ev)
		}
	}
}

func (e *Engine) current() *turnState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cur
}

func (e *Engine) finishTurn() {
	e.mu.Lock()
	e.cur = nil
	e.mu.Unlock()
}

func (e *Engine) abortCurrent() {
	if t := e.current(); t != nil {
		t.cancel()
	}
}

func (e *Engine) notice(level, text string) {
	e.bus.Publish(event.Notice{Level: level, Text: text})
}

// Transcript copies the message log; the Turn goroutine owns the
// original while one is running.
func (e *Engine) Transcript() []event.Message {
	e.trMu.Lock()
	defer e.trMu.Unlock()
	return append([]event.Message(nil), e.tr.messages()...)
}

// Session is this engine's id.
func (e *Engine) Session() uuid.UUID { return e.session }
