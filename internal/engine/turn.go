package engine

import (
	"context"
	"sync"

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

	mu      sync.Mutex
	notes   []string
	stop    string
	aborted bool
}

// post hands an intent to the Turn. Never blocks: a full inbox means
// the Turn is mid-model-call and will drain at the next boundary.
func (t *turnState) post(ev event.Event) {
	select {
	case t.inbox <- ev:
	default:
	}
}

func (e *Engine) startTurn(ctx context.Context, prompt string, done chan struct{}) {
	tctx, cancel := context.WithCancel(ctx)
	e.turns++
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
	}()
}

// runTurn is the loop, blocking and linear. Everything it needs to
// hear about arrives on t.inbox.
func (e *Engine) runTurn(ctx context.Context, t *turnState) {
	t.mark = e.trDo(func() int { return e.tr.mark() })
	e.bus.Publish(event.TurnStarted{Turn: t.id, N: t.n, Prompt: t.prompt})
	e.checkpoint(ctx, t)
	e.appended(t.id, uuid.Nil, func() []event.Message { return e.tr.user(t.prompt) })

	var total event.Usage
	limit := e.maxSteps
	for step := 1; ; step++ {
		t.drain()

		if t.aborted || ctx.Err() != nil {
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
		e.bus.Publish(event.StepEnded{Turn: t.id, Step: stepID, Usage: used, Calls: len(reply.Calls)})
		if reply.Text != "" {
			e.bus.Publish(event.ModelText{Turn: t.id, Step: stepID, Text: reply.Text})
		}

		// No calls means the model is finished asking.
		if len(reply.Calls) == 0 {
			e.appended(t.id, stepID, func() []event.Message { return e.tr.say(reply.Text) })
			e.endTurn(t, event.EndDone, reply.Text, total)
			return
		}
		answers := e.runStep(ctx, t, stepID, reply)
		e.appended(t.id, stepID, func() []event.Message { return e.tr.step(reply, answers) })
	}
}

// endTurn flushes queued notes before closing, or a correction typed
// as the last Step finished never reaches the next Turn.
func (e *Engine) endTurn(t *turnState, why event.EndReason, summary string, used event.Usage) {
	t.drain()
	for _, n := range t.takeNotes() {
		e.appended(t.id, uuid.Nil, func() []event.Message { return e.tr.note(n) })
	}
	e.bus.Publish(event.TurnEnded{Turn: t.id, Reason: why, Summary: summary, Usage: used})
}

// checkpoint takes the Turn's one snapshot, before any Call runs.
func (e *Engine) checkpoint(ctx context.Context, t *turnState) {
	if s, ok := e.snapshotter(); ok {
		if id, err := s.Snapshot(ctx); err == nil {
			t.snap = id
		}
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
			if t.aborted {
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
		t.mu.Lock()
		t.aborted = true
		t.mu.Unlock()
		t.cancel()
	case event.RequestStop:
		t.mu.Lock()
		t.stop = v.Reason
		t.mu.Unlock()
	case event.NoteContext:
		t.mu.Lock()
		t.notes = append(t.notes, v.Text)
		t.mu.Unlock()
	}
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

// compact announces itself first, since summarising stalls the Turn,
// and publishes what it replaced so a replay rebuilds the same one.
func (e *Engine) compact(ctx context.Context, t *turnState) {
	if !e.wouldCompact() {
		return
	}
	e.bus.Publish(event.Notice{Level: "info", Text: "compacting the transcript"})

	var dropped int
	var note string
	e.trLock(func() { dropped, note = e.tr.compact(ctx, e.contextTokens, e.summarizer) })
	if dropped > 0 {
		e.bus.Publish(event.Compacted{Turn: t.id, Dropped: dropped, Note: note})
	}
}

// budget is the transcript ceiling, resolved the way compact resolves
// it, so a front-end measures against what actually applies.
func (e *Engine) budget() int {
	if e.contextTokens <= 0 {
		return DefaultContextTokens
	}
	return e.contextTokens
}

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

func (e *Engine) trDo(fn func() int) int {
	e.trMu.Lock()
	defer e.trMu.Unlock()
	return fn()
}
