package agent

import (
	"context"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/usage"
)

// Confirmer approves the exact command text. Only shown when
// PreJudgment flags Dangerous; nil fails the session closed.
type Confirmer interface {
	Confirm(req ConfirmRequest) bool
}

// Runner executes a command; host.Shell is the unsandboxed default.
// events may be nil (RunGoal's blocking path has no live listener).
type Runner interface {
	Run(ctx context.Context, command string, events chan<- host.StreamEvent) (host.Result, error)
}

// Proposer proposes the next step, plus what the call consumed.
type Proposer interface {
	Propose(ctx context.Context, messages []propose.Message) (propose.Proposal, usage.Usage, error)
}

// Judge returns typed judgments. probe.Judge is a separate,
// identically shaped declaration — probe can't import agent back.
type Judge interface {
	Ask(ctx context.Context, state classify.State, questions classify.Questions) (classify.Answers, classify.Usage, error)
}
