package viewgen

import (
	"context"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/usage"
)

// Structurer asks a model for JSON matching a schema. Declared here
// because viewgen is what calls it; propose.OpenAIProposer satisfies
// it structurally, so this package never imports the proposer.
type Structurer interface {
	Structured(ctx context.Context, system, user string, schema map[string]any) ([]byte, usage.Usage, error)
}

// Judge is the same shape agent.Judge and probe.Judge have. Declared
// again rather than imported: agent imports viewgen, so viewgen
// importing agent back would be a cycle.
type Judge interface {
	Ask(ctx context.Context, state classify.State, questions classify.Questions) (classify.Answers, classify.Usage, error)
}
