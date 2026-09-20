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

// RunnerSelector picks which Runner executes a command. Select must be
// pure and idempotent; it may be called more than once per step.
type RunnerSelector interface {
	Select(pre PreJudgment) (runner Runner, mode string)
	// Probe always returns the real-host runner; probes need the
	// actual environment, not a sandboxed view of it.
	Probe() Runner
	// Sandbox returns the configured sandbox Runner, or nil.
	Sandbox() Runner
}

// Snapshotter is an optional Runner capability for a point-in-time
// checkpoint of its environment. host.Shell doesn't implement it.
type Snapshotter interface {
	Snapshot(ctx context.Context) (SnapshotID, error)
	Rollback(ctx context.Context, id SnapshotID) error
}

// SnapshotID identifies one checkpoint taken by a Snapshotter.
type SnapshotID string

// Proposer proposes the next step, plus what the call consumed.
type Proposer interface {
	Propose(ctx context.Context, messages []propose.Message) (propose.Proposal, usage.Usage, error)
}

// Judge returns typed judgments. probe.Judge is a separate,
// identically shaped declaration — probe can't import agent back.
type Judge interface {
	Ask(ctx context.Context, state classify.State, questions classify.Questions) (classify.Answers, classify.Usage, error)
}
