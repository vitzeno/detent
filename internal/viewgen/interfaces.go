package viewgen

import (
	"context"

	"github.com/vitzeno/detent/internal/classify"
)

// Judge is the same shape agent.Judge and probe.Judge have. Declared
// again rather than imported: agent imports viewgen, so viewgen
// importing agent back would be a cycle.
type Judge interface {
	Ask(ctx context.Context, state classify.State, questions classify.Questions) (classify.Answers, classify.Usage, error)
}
