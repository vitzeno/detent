package viewgen

import (
	"context"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// Watch composes a view per judged Call and per Shell. A subscriber, so
// a redraw cannot fire a second model call: a redraw is not an event.
func (g *Generator) Watch(bus *event.Bus) func() {
	facts, stop := bus.Subscribe(event.Only(
		event.TurnStartedKind, event.CallProposedKind,
		event.CallEndedKind, event.CallJudgedKind,
		event.ShellStartedKind, event.ShellEndedKind))
	go func() {
		w := watcher{gen: g, bus: bus, calls: map[uuid.UUID]*pending{},
			shells: map[uuid.UUID]string{}, slots: make(chan struct{}, maxComposing)}
		for rec := range facts {
			w.take(rec.Event)
		}
	}()
	return stop
}

// pending is what is known about one Call so far. Composition needs
// the command, the output and the judged kind, which arrive in three
// separate facts.
type pending struct {
	command string
	result  *event.Result
	kind    string
}

type watcher struct {
	gen   *Generator
	bus   *event.Bus
	calls map[uuid.UUID]*pending
	// shells is the command each running Shell was started with.
	shells map[uuid.UUID]string
	// slots caps concurrent composition: parallel Calls finish
	// together and each costs a judge round trip.
	slots chan struct{}
}

func (w *watcher) take(e event.Event) {
	switch v := e.(type) {
	case event.TurnStarted:
		clear(w.calls) // nothing from a finished Turn can still compose
	case event.CallProposed:
		// The command, not the tool: every bash call keyed as "bash" once.
		w.calls[v.Call] = &pending{command: event.Command(v.Tool, v.Args)}
	case event.CallEnded:
		if p := w.calls[v.Call]; p != nil {
			result := v.Result
			p.result = &result
		}
	case event.CallJudged:
		p := w.calls[v.Call]
		if p == nil || p.result == nil {
			return
		}
		p.kind = v.RenderKind
		delete(w.calls, v.Call) // composed once, whatever happens next
		go w.compose(v.Call, *p)
	case event.ShellStarted:
		w.shells[v.Shell] = v.Command
	case event.ShellEnded:
		command, ok := w.shells[v.Shell]
		if !ok {
			return
		}
		delete(w.shells, v.Shell)
		result := v.Result
		go w.shell(v.Shell, pending{command: command, result: &result})
	}
}

func (w *watcher) compose(call uuid.UUID, p pending) {
	w.slots <- struct{}{}
	defer func() { <-w.slots }()
	if got, ok := w.resolve(context.Background(), p); ok {
		w.publish(call, got)
	}
}

// shell draws a command the human ran. Nothing judged it, so the shape
// is asked here, and only the shape: never how it went.
func (w *watcher) shell(id uuid.UUID, p pending) {
	w.slots <- struct{}{}
	defer func() { <-w.slots }()
	ctx := context.Background()
	p.kind = w.gen.renderKind(ctx, p.command, outputOf(p.result))
	got, ok := w.resolve(ctx, p)
	if !ok {
		// A Call's row falls back to its kind's view in ui; a Shell's
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
	got, err := w.gen.Compose(ctx, req)
	return got, err == nil && got.Spec != nil
}

func (w *watcher) request(p pending) Request {
	return Request{Command: p.command, Output: outputOf(p.result),
		ExitCode: p.result.ExitCode, Kind: p.kind}
}

func (w *watcher) publish(call uuid.UUID, got Result) {
	w.bus.Publish(event.ViewReady{Call: call, Spec: got.Spec, Source: string(got.Source)})
}

const maxComposing = 2

func outputOf(r *event.Result) string {
	switch {
	case r.Stdout != "" && r.Stderr != "":
		return r.Stdout + "\n" + r.Stderr
	case r.Stderr != "":
		return r.Stderr
	}
	return r.Stdout
}
