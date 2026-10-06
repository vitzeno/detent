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

const (
	// DefaultCommandTimeout bounds a tool call nobody stops sooner.
	DefaultCommandTimeout = 10 * time.Minute
	// DefaultStopGrace is how long a cancelled Run waits for the running
	// Turn to publish its last facts before giving up on them.
	DefaultStopGrace = 5 * time.Second
	// DefaultContextTokens caps the transcript, resent whole every Step.
	// Sized for a large window, so a small local model wants context_tokens.
	DefaultContextTokens = 200_000
)

const (
	// defaultMaxSteps is soft: hitting it asks rather than stops, because a human is watching.
	defaultMaxSteps = 100
	// defaultToolCallsPerStep is small since compaction never splits a Step's results.
	defaultToolCallsPerStep = 10
	// defaultRepeatLimit is how often one command may print the same thing in a Turn.
	defaultRepeatLimit = 3
	// maxNudges is how many unfinished replies in a row are told to carry on.
	maxNudges = 2
)

// Engine is one session. Run it once, in its own goroutine, and
// everything else reaches it through the bus.
type Engine struct {
	bus     *event.Bus
	runners RunnerSelector
	// root is the agent answering the human, built in New once the options are in.
	root *agent

	// extra is the hooks a caller added, which every agent's chain ends with.
	extra   []Assessor
	judge   *jevHook
	invoker Invoker

	// childModel runs subagents, and without one spawn_agent answers that
	// there are none. The limits below bound each child.
	childModel   Completer
	maxAgents    int
	childContext int
	childTimeout time.Duration
	// slots holds one token per running child.
	slots chan struct{}

	session uuid.UUID
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
	// sessions is the stored log a resume reads, and resumeNote what it tells the model.
	sessions   SessionLog
	resumeNote func([]event.Record) string
	// settled is the tree as the last Turn left it, so a rollback can tell
	// the human's later edits from the Turn's own.
	settled string

	// How this run describes itself, for the fact published at start.
	modelName string
	judgeName string
	network   bool
	recorded  bool
	resumed   int
	// leftOpen is what a resumed session never finished, ended as Run starts.
	leftOpen openWork
	// instructions name the files the prompt carries, for SessionStarted.
	instructions []string
	commit       string
	skills       []event.SkillSummary

	// gauge turns bytes into tokens, learning the ratio from each Step.
	gauge gauge
	// lingerAfterTurn widens the gap between a Turn's end and its goroutine's, for tests.
	lingerAfterTurn func()
	// afterAnnounce runs once a child is announced, for tests.
	afterAnnounce func(uuid.UUID)

	// intents is subscribed in New, not Run, so a caller publishing the
	// moment New returns cannot lose it.
	intents <-chan event.Record
	unsub   func()

	// resetting is a reset waiting on the Turn it aborted. Run goroutine only.
	resetting bool
	// past is what can still be undone. Run goroutine only.
	past map[uuid.UUID]*turnState

	// mu guards cur, warned and agents, which the Turn goroutine reads as well.
	mu sync.Mutex
	// cur is the Turn in flight.
	cur *turnState
	// warned names the hooks that already failed this Turn, so an outage warns once.
	warned map[string]bool
	// agents is each running child's stop, for StopAgent.
	agents map[uuid.UUID]context.CancelCauseFunc
}

// New builds an Engine and subscribes it to intents. Run starts it, and
// an Engine never run holds its subscription until the bus closes.
func New(bus *event.Bus, m Completer, tools *tool.Registry, runners RunnerSelector, opts ...Option) *Engine {
	e := &Engine{
		bus: bus, runners: runners,
		session:        uuid.Must(uuid.NewV7()),
		maxSteps:       defaultMaxSteps,
		maxToolCalls:   defaultToolCallsPerStep,
		commandTimeout: DefaultCommandTimeout,
		finishCheck:    true,
		contextTokens:  DefaultContextTokens,
		stopGrace:      DefaultStopGrace,
		past:           map[uuid.UUID]*turnState{},
		maxAgents:      DefaultMaxAgents,
		childContext:   DefaultChildContextTokens,
		childTimeout:   DefaultChildTimeout,
		slots:          make(chan struct{}, childSlots),
		agents:         map[uuid.UUID]context.CancelCauseFunc{},
	}
	for _, o := range opts {
		o(e)
	}
	// A child's budget is a share of the window, never more than all of it.
	e.childContext = min(e.childContext, e.contextTokens)
	// The network hook last.
	if e.judge != nil {
		e.extra = append(e.extra, e.judge)
	}
	e.root = newAgent(uuid.Nil, "", m, tools, e.extra...)
	e.intents, e.unsub = bus.Subscribe(event.Intents())
	return e
}

// Run is the actor loop. It reads intents and nothing else, and every
// fact it produces goes out on the bus.
func (e *Engine) Run(ctx context.Context) {
	defer e.unsub()
	// Read before SessionStarted goes out, since anything may follow that.
	open := e.leftOpen
	e.started()
	e.endLeftOpen(open)
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

// Completer is one model round trip: a Step.
type Completer interface {
	Complete(ctx context.Context, msgs []event.Message, tools []map[string]any) (model.Reply, event.Usage, error)
}

// Runner executes one command, satisfied structurally by host.Shell and sandbox.Container.
// It sends nothing on events after it returns, and the caller closes events.
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

// SessionLog replays a stored session, satisfied structurally by store.Store.
type SessionLog interface {
	Replay(session uuid.UUID) ([]event.Record, error)
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
		e.appended(e.root, uuid.Nil, uuid.Nil, func() []event.Message { return e.root.tr.note(v.Text) })
	case event.ResetSession:
		// Aborted now, not queued, and reset once the Turn has ended.
		if t != nil {
			e.resetting = true
			t.absorb(event.Abort{Turn: t.id})
			return
		}
		e.reset()
	case event.ResumeSession:
		if t != nil {
			e.notice("warn", "cannot resume another session while a request is running")
			return
		}
		e.resume(v.Session)
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
	case event.SuggestFinish:
		if t != nil {
			e.notice("info", "the judge reads the request as answered, so the model was told")
			e.post(t, ev)
		}
	case event.Continue:
		if t != nil {
			e.post(t, ev)
		}
	case event.StopAgent:
		// Not queued: the root may be blocked on that very child.
		e.stopAgent(v.Agent)
	case event.ResolveApproval:
		// Straight to its waiter: the Turn may be blocked where it reads no inbox.
		if t != nil {
			t.answer(v)
		}
	}
}

// stale reports an intent meant for another Turn. The judge's advice
// arrives late and asynchronously, and must not reach the next request.
func stale(ev event.Event, turn uuid.UUID) bool {
	var named uuid.UUID
	switch v := ev.(type) {
	case event.Abort:
		named = v.Turn
	case event.SuggestFinish:
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

// reset starts a new session under a new id, leaving the old one stored
// as it was, to resume.
func (e *Engine) reset() {
	e.root.lock(func() { e.root.tr.reset() })
	e.root.repeat.forget()
	e.turns = 0
	e.settled = ""
	e.mu.Lock()
	clear(e.past)
	e.mu.Unlock()
	e.session, e.resumed = uuid.Must(uuid.NewV7()), 0
	e.started()
	e.bus.Publish(e.measure(0))
	e.notice("info", "new session")
}

// started describes this session, which is also what moves the store onto it.
func (e *Engine) started() {
	_, mode := e.runners.Select(event.UnknownRisk())
	e.bus.Publish(event.SessionStarted{
		Session: e.session, Model: e.modelName, Judge: e.judgeName,
		Sandbox: mode == "sandbox",
		Network: e.network, MaxSteps: e.maxSteps,
		Recorded: e.recorded, Resumed: e.resumed,
		ContextTokens: e.budget(), Instructions: e.instructions, Skills: e.skills,
		Commit: e.commit, Subagents: e.childModel != nil, MaxAgents: e.offered(),
		ChildContextTokens: e.childContext,
	})
}

// messages copies the root's log, since the Turn goroutine owns the
// original while one is running.
func (e *Engine) messages() []event.Message { return e.root.messages() }

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

// offered is how many subagents a Turn may start, zero without any.
func (e *Engine) offered() int {
	if e.childModel == nil {
		return 0
	}
	return e.maxAgents
}

// noticeFrom names a child in its notices, since several may be running.
func (e *Engine) noticeFrom(a *agent, level, text string) {
	if !a.root() {
		text = a.name + ": " + text
	}
	e.notice(level, text)
}
