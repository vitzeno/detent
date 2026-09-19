// Package agent proposes, confirms, executes, and observes commands one goal at a time.
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/shell"
	"github.com/vitzeno/detent/internal/usage"
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
	History    []*ExecutedCommand
	GoalsDone  int
}

// ConfirmFunc approves the exact command text. Only shown for a command
// PreJudgment flags Dangerous (Jev's mutability/scope_risk escalation,
// or the FlagDanger regex backstop); every other command runs straight
// through — a nil ConfirmFunc still fails the whole session closed,
// since something must be wired for the commands that do need it.
// A nil ConfirmFunc fails closed.
type ConfirmFunc func(req ConfirmRequest) bool

// ExecutedCommand is one approved command plus its outcome; nil Post means judgment pending.
type ExecutedCommand struct {
	Command string
	Result  shell.Result
	Pre     PreJudgment
	Post    *PostJudgment
	// Usage links the measured step; nil when untracked.
	Usage *usage.Step
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
// Commands holds pointers so a caller (the TUI) can keep a reference to
// one entry — e.g. to attach Post once judgment lands later — that stays
// valid across further appends to the slice.
type GoalResult struct {
	Goal     string
	Commands []*ExecutedCommand
	Summary  string
	End      EndReason
	// Stats links the measured goal; nil when untracked.
	Stats *usage.Goal
}

// RunFunc executes a command, defaulting to shell.Stream.
type RunFunc func(ctx context.Context, command string, onEvent func(shell.StreamEvent)) (shell.Result, error)

// Proposer proposes the next step for the currently open goal, plus
// what the call consumed. Declared here, not in propose, since this is
// the only place it's consumed — propose ships just the data types
// (Message, Proposal) plus OpenAIProposer, its one implementation.
type Proposer interface {
	Propose(ctx context.Context, messages []propose.Message) (propose.Proposal, usage.Usage, error)
}

// Judge returns typed judgments; Ask batches one iteration into a
// single call. Declared here rather than in classify for the same
// reason as Proposer above; probe.Judge is a separate, identically
// shaped declaration at probe's own boundary, since probe sits below
// agent and can't import back up to it.
type Judge interface {
	Ask(ctx context.Context, state classify.State, questions classify.Questions) (classify.Answers, classify.Usage, error)
}

// Session is one running instance with an append-only transcript across goals.
type Session struct {
	Proposer Proposer
	Confirm  ConfirmFunc
	Run      RunFunc

	// Judge is a classifier only; nil disables both batches.
	Judge         Judge
	RiskThreshold float64

	// StepBudget caps iterations per goal; <=0 means unbounded.
	StepBudget int

	// Stats receives timings and token counts; nil disables tracking.
	Stats *usage.Tracker

	Transcript []propose.Message
	GoalsDone  int
}

// Option configures a Session. Only set what differs: no Judge, no
// cap, and no tracking unless asked.
type Option func(*Session)

func WithRun(run RunFunc) Option {
	return func(s *Session) { s.Run = run }
}

func WithJudge(judge Judge) Option {
	return func(s *Session) { s.Judge = judge }
}

func WithRiskThreshold(t float64) Option {
	return func(s *Session) { s.RiskThreshold = t }
}

func WithStepBudget(n int) Option {
	return func(s *Session) { s.StepBudget = n }
}

func WithStats(t *usage.Tracker) Option {
	return func(s *Session) { s.Stats = t }
}

// New builds a session around a proposer and confirm function.
func New(proposer Proposer, confirm ConfirmFunc, opts ...Option) *Session {
	s := &Session{Proposer: proposer, Confirm: confirm}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Tracker exposes the usage tracker for front-ends rendering stats.
func (s *Session) Tracker() *usage.Tracker {
	if s == nil {
		return nil
	}
	return s.Stats
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

// finish closes res as reason and syncs GoalsDone with it, so every
// terminal path (Record*, RecordAbort, Execute's own failure path)
// updates both exactly once instead of each hand-rolling the same
// two-step bookkeeping — see the finding in the review that RunGoal's
// no-Confirm bypass and Execute's error path used to disagree on it.
func (s *Session) finish(res *GoalResult, reason EndReason, summary string) {
	res.End = reason
	res.Stats.Finish(string(reason), summary, reason == EndDeclined)
	s.GoalsDone++
}

func formatToolResult(command string, r shell.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Command `%s` exited with code %d.", command, r.ExitCode)
	bounded := func(name, s string) {
		if s == "" {
			return
		}
		fmt.Fprintf(&b, "\n%s:\n%s", name, boundStr(s))
	}
	bounded("stdout", r.Stdout)
	bounded("stderr", r.Stderr)
	if r.Truncated {
		b.WriteString("\n[output truncated at capture]")
	}
	return b.String()
}

// boundStr caps s at MaxTranscriptOutputBytes, the same discipline
// every transcript entry (command output, probe output, a saved
// file's diff) follows so no single turn can blow the context budget.
func boundStr(s string) string {
	if len(s) <= MaxTranscriptOutputBytes {
		return s
	}
	return s[:MaxTranscriptOutputBytes] + "\n…[truncated]"
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
