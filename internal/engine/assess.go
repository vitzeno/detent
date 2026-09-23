package engine

import (
	"context"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/tool"
)

// Assessor is the one extension point off the bus: ordered,
// synchronous, and able to change what happens next.
type Assessor interface {
	Name() string
	// Assess returns what it knows. The chain folds it with Widen, so
	// returning a Risk that says "safe" cannot make anything safer.
	Assess(ctx context.Context, c tool.Call, current event.Risk) (event.Risk, error)
}

// assess folds the chain in order, cheap hooks first: once one says
// Dangerous the confirm need not wait on the network.
func (e *Engine) assess(ctx context.Context, c tool.Call) event.Risk {
	r := event.UnknownRisk()
	for _, a := range e.assessors {
		got, err := a.Assess(ctx, c, r)
		if err != nil {
			// A hook that fails is skipped, not fatal. It just means
			// nobody answered, which the heuristics already handle.
			e.notice("warn", a.Name()+" could not assess: "+err.Error())
			continue
		}
		r = r.Widen(got)
	}
	return r
}
