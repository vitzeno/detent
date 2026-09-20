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

// Confirmer approves the exact command text. Only shown for a command
// PreJudgment flags Dangerous. A nil Confirmer fails the whole session
// closed — the commands that need it need something wired.
type Confirmer interface {
	Confirm(req ConfirmRequest) bool
}

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
// Commands holds pointers so a caller can keep a reference to one entry
// (e.g. to attach Post later) that stays valid across further appends.
type GoalResult struct {
	Goal     string
	Commands []*ExecutedCommand
	Summary  string
	End      EndReason
	// Stats links the measured goal; nil when untracked.
	Stats *usage.Goal
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

// shellRunner adapts shell.Stream's plain callback to StreamSink.
type shellRunner struct{}

func (shellRunner) Run(ctx context.Context, command string, sink StreamSink) (shell.Result, error) {
	var onEvent func(shell.StreamEvent)
	if sink != nil {
		onEvent = sink.OnEvent
	}
	return shell.Stream(ctx, command, onEvent)
}

// streamSinkFunc bridges probe.Run's own plain-callback signature to
// StreamSink (probes never stream, so this always wraps nil).
type streamSinkFunc func(shell.StreamEvent)

func (f streamSinkFunc) OnEvent(e shell.StreamEvent) {
	if f != nil {
		f(e)
	}
}

// Proposer proposes the next step for the currently open goal, plus
// what the call consumed. Declared here, not in propose, since agent is
// its only consumer.
type Proposer interface {
	Propose(ctx context.Context, messages []propose.Message) (propose.Proposal, usage.Usage, error)
}

// Judge returns typed judgments; Ask batches one iteration into a
// single call. probe.Judge is a separate, identically shaped
// declaration — probe sits below agent and can't import it back up.
type Judge interface {
	Ask(ctx context.Context, state classify.State, questions classify.Questions) (classify.Answers, classify.Usage, error)
}

// Session is one running instance with an append-only transcript across goals.
type Session struct {
	Proposer Proposer
	Confirm  Confirmer
	Run      Runner

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

func WithRun(run Runner) Option {
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

// New builds a session around a proposer and confirmer.
func New(proposer Proposer, confirm Confirmer, opts ...Option) *Session {
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

func (s *Session) runner() Runner {
	if s.Run != nil {
		return s.Run
	}
	return shellRunner{}
}

func (s *Session) append(m propose.Message) {
	s.Transcript = append(s.Transcript, m)
}

// finish closes res as reason and syncs GoalsDone — every terminal path
// (Record*, RecordAbort, Execute's failure path) goes through here so
// both update exactly once.
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

// boundStr caps s at MaxTranscriptOutputBytes so no single transcript
// entry can blow the context budget.
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
