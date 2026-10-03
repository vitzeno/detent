// Package engine owns the agent loop, and drives itself. That is what
// lets front-ends subscribe rather than call.
package engine

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// Defaults. MaxSteps is generous and soft: hitting it asks rather than
// stops, because a human is watching.
const (
	DefaultMaxSteps = 100
	// A Step is never split by compaction, so its results must fit the budget whole.
	DefaultToolCallsPerStep = 10
	DefaultRepeatLimit      = 3
	// How many Unfinished replies in a row are nudged before one is taken as the end.
	DefaultNudges = 2
	// A human can stop a command sooner. This bounds the one nobody watches.
	DefaultCommandTimeout = 10 * time.Minute
)

// DefaultStopGrace is how long a cancelled Run waits for the Turn it
// was running to publish its last facts before giving up on them.
const DefaultStopGrace = 5 * time.Second

// Engine is one session. Run it once, in its own goroutine, and
// everything else reaches it through the bus.
type Engine struct {
	bus     *event.Bus
	model   Completer
	tools   *tool.Registry
	runners RunnerSelector

	assessors []Assessor
	judge     *jevHook
	invoker   Invoker
	repeat    *repeatHook

	session uuid.UUID
	tr      transcript
	turns   int

	maxSteps     int
	maxToolCalls int
	// commandTimeout bounds every tool call, host or sandbox alike.
	commandTimeout time.Duration
	// finishCheck asks once for a check before a Turn that changed something ends.
	finishCheck   bool
	contextTokens int
	summarizer    Summarizer
	stopGrace     time.Duration

	worktreer Worktreer
	// settled is the tree as the last Turn left it, so a rollback can tell
	// the human's later edits from the Turn's own.
	settled string

	// How this run describes itself, for the fact published at start.
	modelName string
	judgeName string
	network   bool
	recorded  bool
	resumed   int
	// instructions name the files the prompt carries, for SessionStarted.
	instructions []string
	skills       []event.SkillSummary

	// gauge turns bytes into tokens, learning the ratio from each Step.
	gauge gauge
	// lingerAfterTurn widens the gap between a Turn's end and its goroutine's, for tests.
	lingerAfterTurn func()

	// intents is subscribed in New, not Run, so a caller publishing the
	// moment New returns cannot lose it.
	intents <-chan event.Record
	unsub   func()

	// trMu guards the transcript, which the Turn goroutine writes and
	// a caller may read at any moment.
	trMu sync.Mutex

	// resetting is a reset waiting on the Turn it aborted. Run goroutine only.
	resetting bool

	mu sync.Mutex
	// cur is the Turn in flight, past is what can still be undone.
	cur  *turnState
	past map[uuid.UUID]*turnState
	// warned names the hooks that already failed this Turn, so an outage warns once.
	warned map[string]bool
}

// New builds an Engine and subscribes it to intents. Run starts it, and
// an Engine never run holds its subscription until the bus closes.
func New(bus *event.Bus, m Completer, tools *tool.Registry, runners RunnerSelector, opts ...Option) *Engine {
	e := &Engine{
		bus: bus, model: m, tools: tools, runners: runners,
		session:        uuid.Must(uuid.NewV7()),
		maxSteps:       DefaultMaxSteps,
		maxToolCalls:   DefaultToolCallsPerStep,
		commandTimeout: DefaultCommandTimeout,
		finishCheck:    true,
		contextTokens:  DefaultContextTokens,
		stopGrace:      DefaultStopGrace,
		repeat:         newRepeatHook(DefaultRepeatLimit),
		past:           map[uuid.UUID]*turnState{},
	}
	for _, o := range opts {
		o(e)
	}
	// Cheapest first, the network hook last.
	e.assessors = append([]Assessor{toolFloor{}, mcpFloor{}, regexHook{}, e.repeat}, e.assessors...)
	if e.judge != nil {
		e.assessors = append(e.assessors, e.judge)
	}
	e.intents, e.unsub = bus.Subscribe(event.Intents())
	return e
}

// Run is the actor loop. It reads intents and nothing else, and every
// fact it produces goes out on the bus.
func (e *Engine) Run(ctx context.Context) {
	defer e.unsub()
	_, mode := e.runners.Select(event.UnknownRisk())
	e.bus.Publish(event.SessionStarted{
		Session: e.session, Model: e.modelName, Judge: e.judgeName,
		Sandbox: mode == "sandbox",
		Network: e.network, MaxSteps: e.maxSteps,
		Recorded: e.recorded, Resumed: e.resumed,
		ContextTokens: e.budget(), Instructions: e.instructions, Skills: e.skills,
	})
	e.bus.Publish(e.measure(0))
	done := make(chan struct{}, 1)

	for {
		select {
		case <-ctx.Done():
			e.stopCurrent(done)
			return
		case <-done:
			e.turnDone(ctx, done)
		case rec, ok := <-e.intents:
			if !ok {
				e.stopCurrent(done)
				return
			}
			// A Turn that published its end is over, whatever its goroutine
			// has left, and one a reset aborted must end before anything else.
			if t := e.current(); t != nil && (t.ended.Load() || e.resetting) {
				select {
				case <-done:
					e.turnDone(ctx, done)
				case <-ctx.Done():
					e.stopCurrent(done)
					return
				}
			}
			e.dispatch(ctx, rec.Event, done)
		}
	}
}

// Transcript copies the message log, since the Turn goroutine owns the
// original while one is running.
func (e *Engine) Transcript() []event.Message {
	e.trMu.Lock()
	defer e.trMu.Unlock()
	return append([]event.Message(nil), e.tr.messages()...)
}

// Session is this engine's id.
func (e *Engine) Session() uuid.UUID { return e.session }

// Completer is one model round trip: a Step.
type Completer interface {
	Complete(ctx context.Context, msgs []event.Message, tools []map[string]any) (model.Reply, event.Usage, error)
}

// Runner executes one command. host.Shell and sandbox.Container both
// satisfy it structurally, so neither imports this package. It sends
// nothing on events after it returns, and the caller closes events.
type Runner interface {
	Run(ctx context.Context, command string, events chan<- capture.StreamEvent) (capture.Result, error)
}

// Invoker answers a tool call that has no command to run. internal/mcp
// satisfies it, so the engine never imports a client.
type Invoker interface {
	Invoke(ctx context.Context, c tool.Call) capture.Result
}

// RunnerSelector picks host or sandbox per tool call.
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
// snapshot never covers. Restore leaves alone what changed since seen.
type Worktreer interface {
	Checkpoint(ctx context.Context) (string, error)
	Restore(ctx context.Context, id, seen string) error
}

// dispatch routes one intent. A running Turn owns the transcript, so
// only an idle engine touches it here.
func (e *Engine) dispatch(ctx context.Context, ev event.Event, done chan struct{}) {
	t := e.current()
	if t != nil && stale(ev, t.id) {
		return
	}
	switch v := ev.(type) {
	case event.SubmitPrompt:
		if t != nil {
			// Typed mid-Turn, a prompt is steering, not a new request.
			e.post(t, v)
			return
		}
		e.startTurn(ctx, v.Text, done)
	case event.NoteContext:
		// Idle, the note goes in now so the next Turn sees it.
		if t != nil {
			e.post(t, v)
			return
		}
		e.appended(uuid.Nil, uuid.Nil, func() []event.Message { return e.tr.note(v.Text) })
	case event.ResetSession:
		// Aborted now, not queued, and reset once the Turn has ended.
		if t != nil {
			e.resetting = true
			t.absorb(event.Abort{Turn: t.id})
			return
		}
		e.reset()
	case event.RequestRollback:
		if t != nil {
			e.notice("warn", "cannot roll back while a request is running")
			return
		}
		e.rollback(ctx, v)
	case event.MeasureContext:
		// Answered now, even mid-Turn: it only reads, under the transcript's lock.
		e.bus.Publish(e.measure(0))
	case event.Abort:
		// Not queued: a blocked tool call never reaches a boundary, and
		// the inbox is only drained at one.
		if t != nil {
			t.absorb(v)
		}
	case event.RequestStop, event.Continue, event.ResolveApproval:
		if t != nil {
			e.post(t, ev)
		}
	}
}

// stale reports an intent meant for another Turn. The judge's stop
// arrives late and asynchronously, and must not end the next request.
func stale(ev event.Event, turn uuid.UUID) bool {
	var named uuid.UUID
	switch v := ev.(type) {
	case event.Abort:
		named = v.Turn
	case event.RequestStop:
		named = v.Turn
	case event.Continue:
		named = v.Turn
	}
	return named != uuid.Nil && named != turn
}

// post hands an intent to the Turn. Never blocks: a full inbox means
// the Turn is mid-model-call and the intent is lost, so it says so.
func (e *Engine) post(t *turnState, ev event.Event) {
	select {
	case t.inbox <- ev:
	default:
		e.notice("warn", "the request was too busy to take a "+string(ev.Kind())+", so it was dropped")
	}
}

// turnDone retires the Turn whose goroutine finished. What reached it
// after its last drain is dispatched again, now to an idle engine.
func (e *Engine) turnDone(ctx context.Context, done chan struct{}) {
	t := e.current()
	e.finishTurn()
	if e.resetting {
		// Everything still queued was sent before the reset, so it goes too.
		e.resetting = false
		e.reset()
		return
	}
	if t == nil {
		return
	}
	for {
		select {
		case ev := <-t.inbox:
			e.dispatch(ctx, ev, done)
		default:
			return
		}
	}
}

// reset forgets the transcript and says so, so a replay forgets it too.
func (e *Engine) reset() {
	e.trLock(func() { e.tr.reset() })
	e.repeat.forget()
	e.turns = 0
	e.settled = ""
	e.mu.Lock()
	clear(e.past)
	e.mu.Unlock()
	e.bus.Publish(event.SessionReset{})
	e.notice("info", "session reset")
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

// stopCurrent aborts the running Turn and waits for what it owes: a
// Step whose results went to a closed bus is one resume cannot send.
func (e *Engine) stopCurrent(done <-chan struct{}) {
	if e.current() == nil {
		return
	}
	e.abortCurrent()
	select {
	case <-done:
	case <-time.After(e.stopGrace):
	}
}

func (e *Engine) abortCurrent() {
	if t := e.current(); t != nil {
		t.cancel()
	}
}

func (e *Engine) notice(level, text string) {
	e.bus.Publish(event.Notice{Level: level, Text: text})
}
