// Package agent proposes, confirms, executes, and observes commands one goal at a time.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/usage"
	"github.com/vitzeno/detent/internal/worktree"
	"github.com/vitzeno/detent/logging"
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
	RunMode    string
	Step       int
	StepBudget int
	History    []*ExecutedCommand
	GoalsDone  int
}

// ExecutedCommand is one approved command plus its outcome; nil Post means judgment pending.
type ExecutedCommand struct {
	Command string
	Result  host.Result
	Pre     PreJudgment
	Post    *PostJudgment
	// SnapshotID is set when this command ran sandboxed and a
	// checkpoint was taken after it; "" otherwise.
	SnapshotID SnapshotID
	// Worktree is the human's own working directory as it stood right
	// after this step, so a rollback can offer to revert it. The
	// container snapshot stops at the bind mount.
	Worktree worktree.Checkpoint
	// TranscriptMark is where the transcript ended right after this
	// step; what Rollback truncates it back to. Counted in appends, not
	// slice positions, so compaction can't move it out from under us.
	TranscriptMark int
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
// Commands holds pointers so a caller can keep a live reference to one.
type GoalResult struct {
	Goal     string
	Commands []*ExecutedCommand
	Summary  string
	End      EndReason
	// Baseline checkpoints the sandbox before step 1 runs, so the
	// first step is undoable like any other; "" when not sandboxed.
	// BaselineMark is the matching Transcript length.
	Baseline     SnapshotID
	BaselineMark int
	// BaselineTree is the working directory before step 1 ran.
	BaselineTree worktree.Checkpoint
	// Stats links the measured goal; nil when untracked.
	Stats *usage.Goal
}

// Session is one running instance with an append-only transcript across goals.
type Session struct {
	// ID identifies this session, e.g. to correlate with a sandbox
	// Runner's own resources. Generated in New unless WithID overrides it.
	ID string

	Proposer Proposer
	Confirm  Confirmer
	Runners  RunnerSelector

	// Judge is a classifier only; nil disables both batches.
	Judge         Judge
	RiskThreshold float64

	// ContextTokens is how much of the model's window the transcript
	// may fill; <=0 means DefaultContextTokens.
	ContextTokens int
	// Summarizer condenses the transcript when it outgrows that
	// budget; nil drops the oldest turns instead.
	Summarizer Summarizer

	// StepBudget caps iterations per goal; <=0 means unbounded.
	StepBudget int

	// Stats receives timings and token counts; nil disables tracking.
	Stats *usage.Tracker

	Transcript []propose.Message
	GoalsDone  int

	// probes is the environment gathered while nobody was waiting.
	// See prefetch.go for why it is filled as a goal closes.
	probes probeCache

	// steps counts executed commands across the session, so a log
	// record carries the same number the UI shows against a row and
	// /rollback takes.
	steps int

	// goalMark is the transcript mark when the open goal began, so
	// ProposeNext can tell "nothing has run for this goal" from
	// "nothing has run at all". One goal is open at a time.
	goalMark int

	// seq counts every message ever appended, and is what a mark
	// records — slice positions move when compaction rewrites the front.
	seq int
	// dropped is how far they have moved: seq-dropped is len(Transcript).
	dropped int
}

// NewSessionID generates a random session identifier. New calls this
// for you; exported so a caller needing the ID first (e.g. to start a
// sandbox Runner) can generate one and pass it to both via WithID.
func NewSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("sess-%d", time.Now().UnixNano()) // practically unreachable
	}
	return hex.EncodeToString(b)
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

func (s *Session) append(m propose.Message) {
	s.Transcript = append(s.Transcript, m)
	s.seq++
}

// mark names the transcript's current end for a step to remember.
func (s *Session) mark() int { return s.seq }

// finish closes res and syncs GoalsDone; every terminal path uses it.
func (s *Session) finish(res *GoalResult, reason EndReason, summary string) {
	res.End = reason
	res.Stats.Finish(string(reason), summary, reason == EndDeclined)
	s.GoalsDone++
	logging.For(logging.Agent).Info("goal closed", logging.KeyEvent, logging.GoalEnd,
		logging.KeyGoal, s.GoalsDone, logging.KeyReason, string(reason),
		"steps", len(res.Commands))
	// The freshest moment to look, and the one nobody waits for.
	// Background: the goal is over, so its cancellation says nothing
	// about whether the next one wants this.
	s.gather(context.Background())
}

func formatToolResult(command string, r host.Result) string {
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

// Reset clears the session back to how it started: no transcript, no
// goals, no usage. The Runners keep running, so a sandbox container
// and anything already done to its filesystem survive — this forgets
// the conversation, it does not rebuild the environment.
func (s *Session) Reset() {
	s.Transcript = nil
	s.GoalsDone = 0
	s.goalMark = 0
	s.seq, s.dropped = 0, 0
	s.Stats.Reset()
}
