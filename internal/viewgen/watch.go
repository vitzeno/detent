package viewgen

import (
	"context"
	"github.com/vitzeno/detent/event"
)

// Watch composes a view per judged Call and publishes it. Being a
// subscriber is what makes the dedup structural: a redraw cannot fire
// a second model call, because a redraw is not an event.
func (g *Generator) Watch(bus *event.Bus) func() {
	facts, stop := bus.Subscribe(event.Only(
		event.TurnStartedKind, event.CallProposedKind,
		event.CallEndedKind, event.CallJudgedKind))
	go func() {
		w := watcher{gen: g, bus: bus, calls: map[event.ID]*pending{},
			slots: make(chan struct{}, maxComposing)}
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
	calls map[event.ID]*pending
	// slots caps concurrent composition: parallel Calls finish
	// together and each costs a judge round trip.
	slots chan struct{}
}

func (w *watcher) take(e event.Event) {
	switch v := e.(type) {
	case event.TurnStarted:
		clear(w.calls) // nothing from a finished Turn can still compose
	case event.CallProposed:
		w.calls[v.Call] = &pending{command: v.Tool}
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
	}
}

func (w *watcher) compose(call event.ID, p pending) {
	w.slots <- struct{}{}
	defer func() { <-w.slots }()

	req := Request{Command: p.command, Output: outputOf(p.result),
		ExitCode: p.result.ExitCode, Kind: p.kind}
	ctx := context.Background()
	if got, ok := w.gen.Existing(ctx, req); ok {
		w.publish(call, got)
		return
	}
	got, err := w.gen.Compose(ctx, req)
	if err != nil || got.Spec == nil {
		return
	}
	w.publish(call, got)
}

func (w *watcher) publish(call event.ID, got Result) {
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
