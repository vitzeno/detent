package engine

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// unfinishedNote answers a reply that ended with neither a call nor an answer.
const unfinishedNote = "[your last reply ended with no answer and no tool call] " +
	"Carry on with the request: call a tool, or give your final answer."

// finishNote is sent once, the first time a Turn that changed something
// would end, since a model tends to check its own reading of a request.
const finishNote = "[before you finish] Re-read the request and check each thing it asks for against what you " +
	"actually produced: run it, test it or measure it. If anything is missing, wrong or unchecked, carry on. " +
	"Otherwise give your final answer again."

// finishSuggestion is the judge's verdict as the model reads it. Advice only,
// since the judge saw outputs and the model knows what it still meant to do.
const finishSuggestion = "[a reviewer reads your request as answered] If it is, give your final answer now. " +
	"If anything is still to do, carry on."

// turnState is one Turn in flight. The Turn goroutine owns the
// transcript while this exists, and the dispatcher only forwards to it.
type turnState struct {
	id     uuid.UUID
	n      int
	prompt string
	// What a rollback restores, all taken before anything ran.
	mark int
	snap string
	tree string

	inbox  chan event.Event
	cancel context.CancelFunc
	// ended is set before TurnEnded is published, so an intent sent on
	// seeing it is not mistaken for one sent while the Turn still ran.
	ended atomic.Bool

	// aborted is written by the dispatcher and read by the Turn goroutine.
	aborted atomic.Bool

	// changed is set once a tool call that is not read-only has run.
	changed atomic.Bool

	mu    sync.Mutex
	notes []string
	// used is every agent's spend this Turn, added from each one's goroutine.
	used event.Usage
	// spawned counts the subagents this Turn started, against maxAgents.
	spawned atomic.Int32
	// waiting holds each open question's answer, keyed by its tool call,
	// so several can wait at once and none takes another's.
	waiting map[uuid.UUID]chan bool
}

func newTurnState(n int, prompt string, cancel context.CancelFunc) *turnState {
	return &turnState{
		id: uuid.Must(uuid.NewV7()), n: n, prompt: prompt,
		inbox: make(chan event.Event, 64), cancel: cancel,
		waiting: map[uuid.UUID]chan bool{},
	}
}

func (e *Engine) startTurn(ctx context.Context, prompt string, done chan struct{}) {
	tctx, cancel := context.WithCancel(ctx)
	e.turns++
	// Repeats are counted per request: the same tests rerun in a later one are the job.
	e.root.repeat.forget()
	t := newTurnState(e.turns, prompt, cancel)
	e.mu.Lock()
	e.cur = t
	e.past[t.id] = t
	e.warned = nil
	e.mu.Unlock()

	go func() {
		defer cancel()
		defer func() { done <- struct{}{} }()
		e.runTurn(tctx, t)
		if e.lingerAfterTurn != nil {
			e.lingerAfterTurn()
		}
	}()
}

// runTurn is what belongs to the Turn rather than to any agent: its
// checkpoint, the prompt, and its end. Everything the root needs to hear
// about arrives on t.inbox.
func (e *Engine) runTurn(ctx context.Context, t *turnState) {
	a := e.root
	a.lock(func() { t.mark = a.tr.mark() })
	e.bus.Publish(event.TurnStarted{Turn: t.id, N: t.n, Prompt: t.prompt})
	e.checkpoint(ctx, t)
	e.appended(a, t.id, uuid.Nil, func() []event.Message { return a.tr.user(t.n, t.prompt) })
	end := e.runAgent(ctx, t, a)
	e.endTurn(ctx, t, end.reason, end.text, t.usage())
}

// agentEnd is how an agent's Steps ended, and its last words. cut says what
// stopped a child short, and used and steps are its own.
type agentEnd struct {
	reason event.EndReason
	text   string
	cut    string
	used   event.Usage
	steps  int
}

// runAgent is the loop, blocking and linear: Steps until the model stops
// asking for tools. What only the root does is said where it happens.
func (e *Engine) runAgent(ctx context.Context, t *turnState, a *agent) (end agentEnd) {
	limit, nudges, checked := e.maxSteps, 0, false
	for step := 1; ; step++ {
		if a.root() {
			t.drain()
		}
		if t.aborted.Load() || ctx.Err() != nil {
			end.reason = event.EndAborted
			return end
		}
		if step > limit && a.root() {
			if !e.askContinue(ctx, t, step-1) {
				end.reason = event.EndBound
				return end
			}
			limit += e.maxSteps
		}
		// A child has hard limits instead: questions from a fan-out arriving
		// together are worse than a partial report.
		if !a.root() && step > childSteps {
			end.reason, end.cut = event.EndBound, fmt.Sprintf("stopped at %d steps", childSteps)
			return end
		}
		if !a.root() && a.over(e.childContext) {
			end.reason, end.cut = event.EndBound, "ran out of context"
			return end
		}
		if a.root() {
			for _, note := range t.takeNotes() {
				e.appended(a, t.id, uuid.Nil, func() []event.Message { return a.tr.note(note) })
			}
			e.compact(ctx, t)
		}

		stepID := uuid.Must(uuid.NewV7())
		e.bus.Publish(event.StepStarted{Turn: t.id, Step: stepID, N: step, Agent: a.id})
		end.steps = step
		reply, used, err := a.model.Complete(ctx, a.messages(), a.tools.Schemas())
		t.addUsage(used)
		end.used = end.used.Add(used)
		if err != nil {
			if ctx.Err() != nil {
				end.reason = event.EndAborted
				return end
			}
			e.noticeFrom(a, "error", err.Error())
			end.reason, end.text = event.EndError, err.Error()
			return end
		}
		e.bus.Publish(event.StepEnded{Turn: t.id, Step: stepID, Usage: used, ToolCalls: len(reply.Requests), Stop: reply.Stop, Agent: a.id})
		if a.root() {
			e.bus.Publish(e.measure(used.PromptTokens))
		}
		if reply.Text != "" {
			e.bus.Publish(event.ModelText{Turn: t.id, Step: stepID, Text: reply.Text, Agent: a.id})
		}

		// No calls means the model is finished asking, unless it stopped mid-thought.
		if len(reply.Requests) == 0 {
			e.appended(a, t.id, stepID, func() []event.Message { return a.tr.say(reply.Text) })
			if reply.Unfinished() && nudges < maxNudges {
				nudges++
				e.noticeFrom(a, "info", fmt.Sprintf("the model stopped with no answer and no call (%s), so it was told to carry on", stopReason(reply.Stop)))
				e.appended(a, t.id, uuid.Nil, func() []event.Message { return a.tr.note(unfinishedNote) })
				continue
			}
			if a.root() && e.finishCheck && t.changed.Load() && !checked {
				checked = true
				e.notice("info", "asked the model to check its work against the request before finishing")
				e.appended(a, t.id, uuid.Nil, func() []event.Message { return a.tr.note(finishNote) })
				continue
			}
			end.reason, end.text = event.EndDone, reply.Text
			return end
		}
		nudges = 0
		answers := e.runStep(ctx, t, a, stepID, reply)
		e.appended(a, t.id, stepID, func() []event.Message { return a.tr.step(reply, answers) })
	}
}

// endTurn flushes queued notes before closing, or a correction typed
// as the last Step finished never reaches the next Turn.
func (e *Engine) endTurn(ctx context.Context, t *turnState, why event.EndReason, summary string, used event.Usage) {
	t.drain()
	tree := e.settle(ctx, t)
	for _, n := range t.takeNotes() {
		e.appended(e.root, t.id, uuid.Nil, func() []event.Message { return e.root.tr.note(n) })
	}
	t.ended.Store(true)
	e.bus.Publish(event.TurnEnded{Turn: t.id, Reason: why, Summary: summary, Usage: used, Tree: tree})
}

// checkpoint takes the Turn's one snapshot, before any tool call runs. One
// that fails is said now, not discovered at undo.
func (e *Engine) checkpoint(ctx context.Context, t *turnState) {
	if s, ok := e.snapshotter(); ok {
		id, err := s.Snapshot(ctx)
		if err != nil && ctx.Err() == nil {
			e.notice("warn", "no checkpoint for this request, so undo cannot restore the sandbox: "+err.Error())
		}
		t.snap = id
	}
	if w, ok := e.worktree(); ok {
		id, err := w.Checkpoint(ctx)
		if err != nil && ctx.Err() == nil {
			e.notice("warn", "no checkpoint of your files for this request, so undo cannot revert them: "+err.Error())
		}
		t.tree = id
	}
	if t.snap != "" || t.tree != "" {
		e.bus.Publish(event.CheckpointTaken{Turn: t.id, Snapshot: t.snap, Tree: t.tree})
	}
}

// settle records the files as this Turn left them, before TurnEnded, so
// a rollback sent on seeing it already knows. An aborted Turn still settles.
func (e *Engine) settle(ctx context.Context, t *turnState) string {
	w, ok := e.worktree()
	if !ok || t.tree == "" {
		return ""
	}
	id, err := w.Checkpoint(context.WithoutCancel(ctx))
	if err != nil {
		e.notice("warn", "could not record your files after this request, so undo may revert later edits: "+err.Error())
	}
	e.settled = id
	return id
}

// askContinue pauses at the bound and waits. Stopping dead would throw
// away a Turn the human might well want finished.
func (e *Engine) askContinue(ctx context.Context, t *turnState, steps int) bool {
	e.bus.Publish(event.BoundReached{Turn: t.id, Steps: steps})
	for {
		select {
		case <-ctx.Done():
			return false
		case ev := <-t.inbox:
			if c, ok := ev.(event.Continue); ok {
				return c.Approved
			}
			t.absorb(ev)
			if t.aborted.Load() {
				return false
			}
		}
	}
}

// drain takes whatever arrived during the last model call.
func (t *turnState) drain() {
	for {
		select {
		case ev := <-t.inbox:
			t.absorb(ev)
		default:
			return
		}
	}
}

// absorb records an intent. Abort cancels immediately, the rest wait
// for a boundary.
func (t *turnState) absorb(ev event.Event) {
	switch v := ev.(type) {
	case event.Abort:
		t.aborted.Store(true)
		t.cancel()
	case event.SuggestFinish:
		t.addNote(finishSuggestion)
	case event.NoteContext:
		t.addNote(v.Text)
	case event.SubmitPrompt:
		t.addNote(v.Text)
	}
}

func (t *turnState) addNote(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.notes = append(t.notes, text)
}

// await registers a question before it is asked, so its answer cannot
// arrive first. The returned func forgets it.
func (t *turnState) await(call uuid.UUID) (<-chan bool, func()) {
	ch := make(chan bool, 1)
	t.mu.Lock()
	t.waiting[call] = ch
	t.mu.Unlock()
	return ch, func() {
		t.mu.Lock()
		delete(t.waiting, call)
		t.mu.Unlock()
	}
}

// answer hands a verdict to whoever waits on its tool call. One naming
// nobody waiting is stale or a repeat, and is dropped.
func (t *turnState) answer(r event.ResolveApproval) {
	t.mu.Lock()
	defer t.mu.Unlock()
	select {
	case t.waiting[r.ToolCall] <- r.Approved:
	default:
	}
}

func (t *turnState) addUsage(u event.Usage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.used = t.used.Add(u)
}

func (t *turnState) usage() event.Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.used
}

func (t *turnState) takeNotes() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.notes
	t.notes = nil
	return out
}

func stopReason(s string) string {
	if s == "" {
		return "no reason given"
	}
	return "stop: " + s
}
