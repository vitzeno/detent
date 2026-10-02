package viewgen

import (
	"context"

	"github.com/vitzeno/detent/internal/classify"
)

// Judge is what composition asks its questions of. classify.JevJudge
// satisfies it, and a test can script one.
type Judge interface {
	Ask(ctx context.Context, state classify.State, questions classify.Questions) (classify.Answers, classify.Usage, error)
}
