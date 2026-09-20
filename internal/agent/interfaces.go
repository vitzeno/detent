package agent

import (
	"context"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/shell"
	"github.com/vitzeno/detent/internal/usage"
)

// Confirmer approves the exact command text. Only shown when
// PreJudgment flags Dangerous; nil fails the session closed.
type Confirmer interface {
	Confirm(req ConfirmRequest) bool
}

// StreamSink receives live output as a command runs. Execute tolerates a
// nil sink (RunGoal's blocking path has no live listener to notify).
type StreamSink interface {
	OnEvent(shell.StreamEvent)
}

// Runner executes a command, defaulting to shellRunner (shell.Stream).
type Runner interface {
	Run(ctx context.Context, command string, sink StreamSink) (shell.Result, error)
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
