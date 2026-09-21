package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vitzeno/detent/viewspec"
)

// This file is ui's own vocabulary for talking to Driver. ui never
// imports agent/usage/propose/host directly; see CLAUDE.md.

// Driver is the session surface ui needs. The one implementation is
// internal/resolver.Resolver, wrapping *agent.Session.
type Driver interface {
	BeginGoal(ctx context.Context, goal string) (*GoalResult, error)
	ProposeNext(ctx context.Context, goal string) (Proposal, PreJudgment, Usage, error)
	// dwell is 0 when no confirm was shown.
	RecordStep(res *GoalResult, p Proposal, pre PreJudgment, proposeUsage Usage, dwell time.Duration) StepHandle
	// events may be nil; Execute never blocks on it (drops under backpressure).
	Execute(ctx context.Context, res *GoalResult, step StepHandle, p Proposal, pre PreJudgment, events chan<- StreamEvent) (*ExecutedCommand, error)
	JudgeResult(ctx context.Context, goal, command string, result Result, step StepHandle) PostJudgment
	RecordDecline(res *GoalResult, command string)
	RecordDone(res *GoalResult, p Proposal)
	RecordProposerError(res *GoalResult, err error)
	// maxBytes is the cap truncated was measured against.
	ReadFile(path string) (content string, truncated bool, maxBytes int, err error)
	// SaveFile persists content and records the change as one operation;
	// diff is what's shown for approval before this is ever called.
	SaveFile(path, diff, content string) error
	// res is nil when the abort landed before BeginGoal produced one.
	RecordAbort(res *GoalResult)
	// Rollback restores the checkpoint after res.Commands[step-1]
	// (1-based) and truncates res.Commands to it. ok is false only
	// when no sandbox is wired; an invalid or non-sandboxed step is a
	// real error to surface.
	// revertFiles also puts the human's own files back; false rolls
	// back the container only.
	Rollback(ctx context.Context, res *GoalResult, step int, revertFiles bool) (ok bool, err error)
	// Reset forgets the session's transcript, goals and usage, so a
	// new goal starts with no history behind it.
	Reset()

	// PlanRollback reports which of the human's own files rolling back
	// to step would touch, without writing any of them.
	PlanRollback(ctx context.Context, res *GoalResult, step int) ([]FileChange, error)

	Tracker() []GoalStats
	UsageSnapshot() Snapshot

	// GenerateView authors a view for a finished command's output, or
	// reports ok=false when nothing beats the render_kind fallback.
	// Blocking: ui calls it from a tea.Cmd, never from Update. A spec
	// that already exists returns without reaching a model at all.
	//
	// No DTO mirror for the spec: viewspec is outside internal/ and
	// both sides import it, so it crosses untranslated.
	GenerateView(ctx context.Context, command, output string, exitCode int, kind RenderKind) (GeneratedView, bool)
}

// GeneratedView is a view the Driver chose for one command's output.
// A struct rather than a bare spec so provenance travels with it: the
// pane says where its framing came from, and a model's is not the
// same as detent's own.
type GeneratedView struct {
	Spec   *viewspec.Spec
	Source ViewSource
}

// ViewSource says how specific to this command the drawn view is, so
// the pane never leaves a human guessing which path produced it.
type ViewSource string

const (
	// ViewBuiltin was chosen by output shape alone, not by command.
	ViewBuiltin ViewSource = "built-in"
	// ViewShipped was written for this command shape.
	ViewShipped ViewSource = "shipped"
	// ViewSaved is on disk, and so editable.
	ViewSaved ViewSource = "saved"
	// ViewGenerated is framing a model wrote this session.
	ViewGenerated ViewSource = "generated"
	// ViewDeclined is not a source but an outcome: a model was asked
	// and nothing it returned survived. The pane keeps its built-in
	// rendering and says so, because a view that was tried and refused
	// looks identical to one never attempted.
	ViewDeclined ViewSource = "declined"
)

// FileChange is one path a rollback would touch in the human's own
// working directory. Removed distinguishes "this file arrived after
// the checkpoint, so reverting deletes it" from "this comes back".
type FileChange struct {
	Path    string
	Removed bool
	// Unseen marks a file changed after detent's last checkpoint —
	// work it never made, which reverting would throw away.
	Unseen bool
}

// StreamEvent is one line of live output from a running command.
type StreamEvent struct {
	Line   string
	Stderr bool
}

// Proposal is one proposed step, or the model's own "done" signal.
type Proposal struct {
	Command   string
	Rationale string
	Done      bool
	Summary   string
	File      string // path Command centers on, if any; "" otherwise
}

// PreJudgment classifies a proposed command before confirm.
type PreJudgment struct {
	FromJudge  bool
	Mutability string // "" means unknown
	ScopeRisk  float64
	Dangerous  bool
	RiskNote   string
	RunMode    string // "host", "sandbox", or "" if unset
}

// RenderKind names the shape of a finished command's output.
type RenderKind string

const (
	KindText    RenderKind = "plain_text"
	KindTable   RenderKind = "table"
	KindFiles   RenderKind = "file_listing"
	KindContent RenderKind = "file_content"
	KindError   RenderKind = "error_text"
	KindDiff    RenderKind = "diff"
	KindJSON    RenderKind = "structured_json"
)

// PostJudgment classifies a finished command for rendering.
type PostJudgment struct {
	FromJudge bool
	// Status is "" or one of "clean_success", "success_with_warnings",
	// "failed", "empty" — see internal/ui/status's Badge/statusWord.
	Status       string
	RenderKind   RenderKind
	Attention    float64 // -1 means unknown
	GoalAchieved float64 // -1 means unknown
}

// Result is a command's captured outcome, mirroring host.Result.
type Result struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Truncated bool
}

func (r Result) Summary() string {
	lines := 0
	for _, s := range []string{r.Stdout, r.Stderr} {
		if s == "" {
			continue
		}
		lines += strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
	}
	out := fmt.Sprintf("exit %d, %d lines", r.ExitCode, lines)
	if r.Truncated {
		out += " (truncated)"
	}
	return out
}

// ExecutedCommand is one approved command plus its outcome; nil Post
// means judgment pending.
type ExecutedCommand struct {
	Command string
	Result  Result
	Pre     PreJudgment
	Post    *PostJudgment
	// SnapshotID is set when this command ran sandboxed; "" otherwise.
	SnapshotID string
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
	Commands []*ExecutedCommand
	Summary  string
	End      EndReason
	// Ref is an opaque token the Driver implementation uses to find its
	// own backing object again on later calls; ui never inspects it.
	Ref any
}

// Usage is one model call's consumption, mirroring usage.Usage.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	Latency          time.Duration
	Model            string
}

// StepHandle correlates a RecordStep call with a later Execute/
// JudgeResult call. Opaque; ui only threads it through.
type StepHandle struct {
	Ref any
}

// StepStats is one proposed command's measured phases, for /usage.
type StepStats struct {
	Command                          string
	Propose, Dwell, Exec, JudgePost  time.Duration
	ExitCode                         int
	ProposerPrompt, ProposerComplete int
	JudgePrompt, JudgeComplete       int
	Attention, GoalAchieved          float64 // -1 when none
	HasPost                          bool
}

// GoalStats is one goal's steps, for /usage.
type GoalStats struct {
	Text  string
	End   string
	Steps []StepStats
}

func (g GoalStats) MachineTime() time.Duration {
	var d time.Duration
	for _, s := range g.Steps {
		d += s.Propose + s.JudgePost + s.Exec
	}
	return d
}

// Snapshot is session-wide usage rollups for the status bar.
type Snapshot struct {
	Goals          int
	Commands       int
	Declined       int
	Propose        time.Duration
	Judge          time.Duration
	Dwell          time.Duration
	Exec           time.Duration
	ProposerTokens int
	JudgeTokens    int
}

func (s Snapshot) MachineTime() time.Duration { return s.Propose + s.Judge + s.Exec }
