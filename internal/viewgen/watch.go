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
func (g *Generator) Watch(bus *event.Bus) func() {
	facts, unsub := bus.Subscribe(event.Only(
		event.TurnStartedKind, event.CallProposedKind,
		event.CallEndedKind, event.CallJudgedKind,
		event.ShellStartedKind, event.ShellEndedKind))
	ctx, cancel := context.WithCancel(context.Background())
	w := &watcher{gen: g, bus: bus, ctx: ctx, calls: map[uuid.UUID]*pending{},
		shells: map[uuid.UUID]string{}, slots: make(chan struct{}, maxComposing)}
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
	// shells is the command each running Shell was started with.
	shells map[uuid.UUID]string
	// slots holds maxComposing: parallel Calls finish together and each
	// costs a judge round trip.
	slots chan struct{}
}

func (w *watcher) take(e event.Event) {
	switch v := e.(type) {
	case event.TurnStarted:
		clear(w.calls) // nothing from a finished Turn can still compose
	case event.CallProposed:
		// The command, not the tool, or every bash call would key as "bash".
		w.calls[v.Call] = &pending{command: event.Command(v.Tool, v.Args)}
	case event.CallEnded:
		p := w.calls[v.Call]
		if p == nil {
			return
		}
		result := v.Result
		p.result = &result
		// With no judge wired no CallJudged follows, so the Call resolves now.
		if w.gen.Unjudged {
			w.start(v.Call, p)
		}
	case event.CallJudged:
		p := w.calls[v.Call]
		if p == nil || p.result == nil {
			return
		}
		p.kind = v.RenderKind
		w.start(v.Call, p)
	case event.ShellStarted:
		w.shells[v.Shell] = v.Command
	case event.ShellEnded:
		command, ok := w.shells[v.Shell]
		if !ok {
			return
		}
		delete(w.shells, v.Shell)
		result := v.Result
		w.spawn(func(ctx context.Context) { w.shell(ctx, v.Shell, pending{command: command, result: &result}) })
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
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ctx, cancel := context.WithTimeout(w.ctx, composeTimeout)
		defer cancel()
		select {
		case w.slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-w.slots }()
		fn(ctx)
	}()
}

func (w *watcher) compose(ctx context.Context, call uuid.UUID, p pending) {
	if got, ok := w.resolve(ctx, p); ok {
		w.publish(call, got)
	}
}

// shell draws a command the human ran. Nothing judged it, so the shape
// is asked here, and only the shape: never how it went.
func (w *watcher) shell(ctx context.Context, id uuid.UUID, p pending) {
	p.kind = w.gen.Shape(ctx, p.command, outputOf(p.result))
	got, ok := w.resolve(ctx, p)
	if !ok {
		// A Call's row falls back to its kind's view in ui, but a Shell's
		// row has no kind there, so the fallback is sent from here.
		got, ok = w.gen.forKind(ctx, w.request(p))
	}
	if ok {
		w.publish(id, got)
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

func (w *watcher) publish(call uuid.UUID, got Result) {
	if w.ctx.Err() != nil {
		return // stopped: nothing is listening for this any more
	}
	w.bus.Publish(event.ViewReady{Call: call, Spec: got.Spec, Source: string(got.Source)})
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
