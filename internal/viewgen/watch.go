package viewgen

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// Watch composes a view per judged Call and per Shell. A subscriber, so
// a redraw cannot fire a second model call: a redraw is not an event.
// Cancelling ctx abandons compositions in flight, as the stop does.
func (g *Generator) Watch(ctx context.Context, bus *event.Bus) func() {
	facts, unsub := bus.Subscribe(event.Only(
		event.TurnStartedKind, event.ToolCallProposedKind,
		event.ToolCallEndedKind, event.ToolCallJudgedKind,
		event.UserCommandStartedKind, event.UserCommandEndedKind))
	ctx, cancel := context.WithCancel(ctx)
	w := &watcher{gen: g, bus: bus, ctx: ctx, calls: map[uuid.UUID]*pending{},
		commands: map[uuid.UUID]string{}, slots: make(chan struct{}, maxComposing)}
	looped := make(chan struct{})
	go func() {
		defer close(looped)
		for rec := range facts {
			w.take(rec.Event)
		}
	}()
	// Stopping cancels compositions in flight and waits for them.
	return func() {
		unsub()
		cancel()
		<-looped
		w.wg.Wait()
	}
}

// pending is what is known about one Call so far: the command, output
// and judged kind arrive in three separate facts.
type pending struct {
	command string
	result  *event.Result
	kind    string
}

// maxComposing caps concurrent composition.
const maxComposing = 2

// composeTimeout bounds one view, waiting for a slot included. The row
// already draws a fallback, so a view this late is not worth the wait.
const composeTimeout = 30 * time.Second

type watcher struct {
	gen *Generator
	bus *event.Bus
	// ctx is cancelled by Watch's stop, and every composition derives from it.
	ctx   context.Context
	wg    sync.WaitGroup
	calls map[uuid.UUID]*pending
	// commands is the command each running Shell was started with.
	commands map[uuid.UUID]string
	// slots holds maxComposing: parallel Calls finish together and each
	// costs a judge round trip.
	slots chan struct{}
}

func (w *watcher) take(e event.Event) {
	switch v := e.(type) {
	case event.TurnStarted:
		clear(w.calls) // nothing from a finished Turn can still compose
	case event.ToolCallProposed:
		// The command, not the tool, or every bash call would key as "bash".
		w.calls[v.ToolCall] = &pending{command: event.Command(v.Tool, v.Args)}
	case event.ToolCallEnded:
		p := w.calls[v.ToolCall]
		if p == nil {
			return
		}
		result := v.Result
		p.result = &result
		// With no judge wired no CallJudged follows, so the Call resolves now.
		if w.gen.Unjudged {
			w.start(v.ToolCall, p)
		}
	case event.ToolCallJudged:
		p := w.calls[v.ToolCall]
		if p == nil || p.result == nil {
			return
		}
		p.kind = v.RenderKind
		w.start(v.ToolCall, p)
	case event.UserCommandStarted:
		w.commands[v.UserCommand] = v.Command
	case event.UserCommandEnded:
		command, ok := w.commands[v.UserCommand]
		if !ok {
			return
		}
		delete(w.commands, v.UserCommand)
		result := v.Result
		w.spawn(func(ctx context.Context) {
			w.userCommand(ctx, v.UserCommand, pending{command: command, result: &result})
		})
	}
}

// start composes a Call's view once, whatever happens next.
func (w *watcher) start(call uuid.UUID, p *pending) {
	delete(w.calls, call)
	got := *p
	w.spawn(func(ctx context.Context) { w.compose(ctx, call, got) })
}

// spawn runs fn on its own goroutine once a slot is free, giving up when
// stopped or when the wait outlasts composeTimeout.
func (w *watcher) spawn(fn func(context.Context)) {
	w.wg.Go(func() {
		ctx, cancel := context.WithTimeout(w.ctx, composeTimeout)
		defer cancel()
		select {
		case w.slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-w.slots }()
		fn(ctx)
	})
}

func (w *watcher) compose(ctx context.Context, call uuid.UUID, p pending) {
	if got, ok := w.resolve(ctx, p); ok {
		w.publish(event.ViewReady{ToolCall: call}, got)
	}
}

// userCommand draws a command the human ran. Nothing judged it, so the shape
// is asked here, and only the shape: never how it went.
func (w *watcher) userCommand(ctx context.Context, id uuid.UUID, p pending) {
	p.kind = w.gen.Shape(ctx, p.command, outputOf(p.result))
	got, ok := w.resolve(ctx, p)
	if !ok {
		// A Call's row falls back to its kind's view in ui, but a Shell's
		// row has no kind there, so the fallback is sent from here.
		got, ok = w.gen.forKind(ctx, w.request(p))
	}
	if ok {
		w.publish(event.ViewReady{UserCommand: id}, got)
	}
}

// resolve is what already exists, then what can be composed.
func (w *watcher) resolve(ctx context.Context, p pending) (Result, bool) {
	req := w.request(p)
	if got, ok := w.gen.Existing(ctx, req); ok {
		return got, true
	}
	got, err := w.gen.compose(ctx, req)
	return got, err == nil && got.Spec != nil
}

func (w *watcher) request(p pending) Request {
	return Request{Command: p.command, Output: outputOf(p.result),
		ExitCode: p.result.ExitCode, Kind: p.kind}
}

// publish sends v, which names what the view is for, with the spec filled in.
func (w *watcher) publish(v event.ViewReady, got Result) {
	if w.ctx.Err() != nil {
		return // stopped: nothing is listening for this any more
	}
	v.Spec, v.Source = got.Spec, string(got.Source)
	w.bus.Publish(v)
}

func outputOf(r *event.Result) string {
	switch {
	case r.Stdout != "" && r.Stderr != "":
		return r.Stdout + "\n" + r.Stderr
	case r.Stderr != "":
		return r.Stderr
	}
	return r.Stdout
}
