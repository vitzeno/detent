package engine

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

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

	// changed is set once a Call that is not read-only has run.
	changed bool

	mu    sync.Mutex
	notes []string
	stop  string
}

func (e *Engine) startTurn(ctx context.Context, prompt string, done chan struct{}) {
	tctx, cancel := context.WithCancel(ctx)
	e.turns++
	// Repeats are counted per request: the same tests rerun in a later one are the job.
	e.repeat.forget()
	t := &turnState{
		id: uuid.Must(uuid.NewV7()), n: e.turns, prompt: prompt,
		inbox: make(chan event.Event, 64), cancel: cancel,
	}
	e.mu.Lock()
	e.cur = t
	e.past[t.id] = t
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

// runTurn is the loop, blocking and linear. Everything it needs to
// hear about arrives on t.inbox.
func (e *Engine) runTurn(ctx context.Context, t *turnState) {
	e.trLock(func() { t.mark = e.tr.mark() })
	e.bus.Publish(event.TurnStarted{Turn: t.id, N: t.n, Prompt: t.prompt})
	e.checkpoint(ctx, t)
	e.appended(t.id, uuid.Nil, func() []event.Message { return e.tr.user(t.n, t.prompt) })

	var total event.Usage
	limit, nudges, checked := e.maxSteps, 0, false
	for step := 1; ; step++ {
		t.drain()

		if t.aborted.Load() || ctx.Err() != nil {
			e.endTurn(t, event.EndAborted, "", total)
			return
		}
		if why := t.takeStop(); why != "" {
			e.endTurn(t, event.EndStopped, why, total)
			return
		}
		if step > limit {
			if !e.askContinue(ctx, t, step-1) {
				e.endTurn(t, event.EndBound, "", total)
				return
			}
			limit += e.maxSteps
		}
		for _, note := range t.takeNotes() {
			e.appended(t.id, uuid.Nil, func() []event.Message { return e.tr.note(note) })
		}
		e.compact(ctx, t)

		stepID := uuid.Must(uuid.NewV7())
		e.bus.Publish(event.StepStarted{Turn: t.id, Step: stepID, N: step})
		reply, used, err := e.model.Complete(ctx, e.Transcript(), e.tools.Schemas())
		total = total.Add(used)
		if err != nil {
			if ctx.Err() != nil {
				e.endTurn(t, event.EndAborted, "", total)
				return
			}
			e.notice("error", err.Error())
			e.endTurn(t, event.EndError, err.Error(), total)
			return
		}
		e.bus.Publish(event.StepEnded{Turn: t.id, Step: stepID, Usage: used, Calls: len(reply.Calls), Stop: reply.Stop})
		e.bus.Publish(e.measure(used.PromptTokens))
		if reply.Text != "" {
			e.bus.Publish(event.ModelText{Turn: t.id, Step: stepID, Text: reply.Text})
		}

		// No calls means the model is finished asking, unless it stopped mid-thought.
		if len(reply.Calls) == 0 {
			e.appended(t.id, stepID, func() []event.Message { return e.tr.say(reply.Text) })
			if reply.Unfinished() && nudges < DefaultNudges {
				nudges++
				e.notice("info", fmt.Sprintf("the model stopped with no answer and no call (%s), so it was told to carry on", stopReason(reply.Stop)))
				e.appended(t.id, uuid.Nil, func() []event.Message { return e.tr.note(unfinishedNote) })
				continue
			}
			if e.finishCheck && t.changed && !checked {
				checked = true
				e.notice("info", "asked the model to check its work against the request before finishing")
				e.appended(t.id, uuid.Nil, func() []event.Message { return e.tr.note(finishNote) })
				continue
			}
			e.endTurn(t, event.EndDone, reply.Text, total)
			return
		}
		nudges = 0
		answers := e.runStep(ctx, t, stepID, reply)
		e.appended(t.id, stepID, func() []event.Message { return e.tr.step(reply, answers) })
	}
}

// unfinishedNote answers a reply that ended with neither a call nor an answer.
const unfinishedNote = "[your last reply ended with no answer and no tool call] " +
	"Carry on with the request: call a tool, or give your final answer."

// finishNote is sent once, the first time a Turn that changed something
// would end, since a model tends to check its own reading of a request.
const finishNote = "[before you finish] Re-read the request and check each thing it asks for against what you " +
	"actually produced: run it, test it or measure it. If anything is missing, wrong or unchecked, carry on. " +
	"Otherwise give your final answer again."

func stopReason(s string) string {
	if s == "" {
		return "no reason given"
	}
	return "stop: " + s
}

// endTurn flushes queued notes before closing, or a correction typed
// as the last Step finished never reaches the next Turn.
func (e *Engine) endTurn(t *turnState, why event.EndReason, summary string, used event.Usage) {
	t.drain()
	for _, n := range t.takeNotes() {
		e.appended(t.id, uuid.Nil, func() []event.Message { return e.tr.note(n) })
	}
	t.ended.Store(true)
	e.bus.Publish(event.TurnEnded{Turn: t.id, Reason: why, Summary: summary, Usage: used})
}

// checkpoint takes the Turn's one snapshot, before any Call runs. One
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
		t.tree = w.Checkpoint(ctx)
	}
	if t.snap != "" || t.tree != "" {
		e.bus.Publish(event.CheckpointTaken{Turn: t.id, Snapshot: t.snap, Tree: t.tree})
	}
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
	case event.RequestStop:
		t.mu.Lock()
		t.stop = v.Reason
		t.mu.Unlock()
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

func (t *turnState) takeNotes() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.notes
	t.notes = nil
	return out
}

func (t *turnState) takeStop() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.stop
	t.stop = ""
	return out
}

func (e *Engine) snapshotter() (Snapshotter, bool) {
	r, _ := e.runners.Select(event.UnknownRisk())
	s, ok := r.(Snapshotter)
	return s, ok
}

// appended mutates the transcript and publishes what went in, so a
// replay puts the same messages back rather than reformatting them.
func (e *Engine) appended(turn, step uuid.UUID, fn func() []event.Message) {
	var added []event.Message
	e.trLock(func() { added = fn() })
	if len(added) > 0 {
		e.bus.Publish(event.Appended{Turn: turn, Step: step, Messages: added})
	}
}

// compact summarises outside the transcript's lock, since that is a model
// call. Only this goroutine mutates the transcript mid-Turn, so the cut holds.
func (e *Engine) compact(ctx context.Context, t *turnState) {
	if !e.wouldCompact() {
		return
	}
	var cut int
	var gone []event.Message
	e.trLock(func() {
		cut = e.tr.cutFor(e.budget())
		gone = append(gone, e.tr.msgs[:cut]...)
	})
	if cut == 0 {
		return
	}
	e.bus.Publish(event.Notice{Level: "info", Text: "compacting the transcript"})
	note := summarise(ctx, e.summarizer, gone)
	e.trLock(func() { e.tr.fold(cut, note) })
	e.bus.Publish(event.Compacted{Turn: t.id, Dropped: cut, Note: note})
}

// budget is the transcript ceiling. New sets a default and the option
// ignores anything below one, so it is always positive.
func (e *Engine) budget() int { return e.contextTokens }

// wouldCompact reports whether the transcript is over budget, so the
// notice is not published for a compact that returns immediately.
func (e *Engine) wouldCompact() bool {
	over := false
	e.trLock(func() { over = e.tr.bytes() > e.budget()*BytesPerToken })
	return over
}

// trLock runs fn holding the transcript lock. Never used around
// anything blocking: a model call takes seconds.
func (e *Engine) trLock(fn func()) {
	e.trMu.Lock()
	defer e.trMu.Unlock()
	fn()
}
