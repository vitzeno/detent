// Package agentloop proposes, confirms, executes, and observes commands one goal at a time.
package agentloop

import (
	"context"
	"fmt"
	"strings"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/shell"
)

// MaxTranscriptOutputBytes caps each recorded output stream.
const MaxTranscriptOutputBytes = 4 * 1024

// RiskThresholdDefault is the scope-risk floor; above it only adds confirm emphasis.
const RiskThresholdDefault = 0.5

// ConfirmRequest is what the confirm step shows for one command.
type ConfirmRequest struct {
	Goal       string
	Command    string
	Rationale  string
	Dangerous  bool
	RiskNote   string
	Mutability string
	Step       int
	StepBudget int
	History    []ExecutedCommand
	GoalsDone  int
}

// ConfirmFunc approves the exact command text; every command requires approval.
// A nil ConfirmFunc fails closed.
type ConfirmFunc func(req ConfirmRequest) bool

// ExecutedCommand is one approved command plus its outcome; nil Post means judgment pending.
type ExecutedCommand struct {
	Command string
	Result  shell.Result
	Pre     PreJudgment
	Post    *PostJudgment
}

// EndReason names how a goal stopped.
type EndReason string

const (
	EndDone           EndReason = "done"
	EndDeclined       EndReason = "declined"
	EndAborted        EndReason = "aborted"
	EndBudget         EndReason = "budget"
	EndProposerError  EndReason = "proposer_error"
	EndConfirmMissing EndReason = "confirm_missing"
)

// GoalResult is what ran for one goal, in order, plus how it ended.
type GoalResult struct {
	Goal     string
	Commands []ExecutedCommand
	Summary  string
	End      EndReason
}

// RunFunc executes a command, defaulting to shell.Stream.
type RunFunc func(ctx context.Context, command string, onEvent func(shell.StreamEvent)) (shell.Result, error)

// Session is one running instance with an append-only transcript across goals.
type Session struct {
	Proposer propose.Proposer
	Confirm  ConfirmFunc
	Run      RunFunc

	// Judge is a classifier only; nil disables both batches.
	Judge         classify.Judge
	RiskThreshold float64

	// StepBudget caps iterations per goal; <=0 means unbounded.
	StepBudget int

	Transcript []propose.Message
	GoalsDone  int
}

func (s *Session) bounded() (int, bool) {
	if s.StepBudget > 0 {
		return s.StepBudget, true
	}
	return 0, false
}

func (s *Session) riskThreshold() float64 {
	if s.RiskThreshold > 0 {
		return s.RiskThreshold
	}
	return RiskThresholdDefault
}

func (s *Session) runFunc() RunFunc {
	if s.Run != nil {
		return s.Run
	}
	return shell.Stream
}

func (s *Session) append(m propose.Message) {
	s.Transcript = append(s.Transcript, m)
}

func formatToolResult(command string, r shell.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Command `%s` exited with code %d.", command, r.ExitCode)
	bounded := func(name, s string) {
		if s == "" {
			return
		}
		if len(s) > MaxTranscriptOutputBytes {
			s = s[:MaxTranscriptOutputBytes] + "\n…[truncated]"
		}
		fmt.Fprintf(&b, "\n%s:\n%s", name, s)
	}
	bounded("stdout", r.Stdout)
	bounded("stderr", r.Stderr)
	if r.Truncated {
		b.WriteString("\n[output truncated at capture]")
	}
	return b.String()
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "; " + b
}
