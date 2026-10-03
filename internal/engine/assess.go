package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/tool"
)

// assessTimeout bounds the whole chain for one Call, since the network
// hook sits between the model asking and anything running.
const assessTimeout = 20 * time.Second

// Assessor is the one extension point off the bus: ordered,
// synchronous, and able to change what happens next.
type Assessor interface {
	Name() string
	// Assess returns what it knows. The chain folds it with Widen, so
	// returning a Risk that says "safe" cannot make anything safer.
	Assess(ctx context.Context, c tool.Call, current event.Risk) (event.Risk, error)
}

// assess folds the chain in order, cheap hooks first. Every hook is
// asked, the network one included, so a flagged Call still gets its scope.
func (e *Engine) assess(ctx context.Context, c tool.Call) event.Risk {
	ctx, cancel := context.WithTimeout(ctx, assessTimeout)
	defer cancel()
	r := event.UnknownRisk()
	for _, a := range e.assessors {
		got, err := assessSafely(ctx, a, c, r)
		if err != nil {
			// A hook that fails is skipped, not fatal. An abort is not
			// a failure worth a warning.
			if !errors.Is(ctx.Err(), context.Canceled) {
				e.notice("warn", a.Name()+" could not assess: "+err.Error())
			}
			continue
		}
		r = r.Widen(got)
	}
	return r
}

// assessSafely turns a panicking hook into a failed one, as runSafely
// does for a Runner.
func assessSafely(ctx context.Context, a Assessor, c tool.Call, r event.Risk) (got event.Risk, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panicked: %v", v)
		}
	}()
	return a.Assess(ctx, c, r)
}
