package probe

import (
	"context"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/shell"
)

// Judge returns typed judgments. Identical in shape to agent.Judge;
// probe can't import agent without a cycle.
type Judge interface {
	Ask(ctx context.Context, state classify.State, questions classify.Questions) (classify.Answers, classify.Usage, error)
}

// Runner executes one command. No sink param like agent.Runner has,
// since probes never stream.
type Runner interface {
	Run(ctx context.Context, command string) (shell.Result, error)
}
