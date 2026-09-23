package event

import (
	"time"

	"github.com/vitzeno/detent/viewspec"
)

// What the engine publishes. Past tense: each says something already
// happened and nobody is being asked anything. The one exception is
// ApprovalAsked, which is a question the engine blocks on — see the
// Bus doc for why that still belongs here.

// A Turn is one human prompt and everything the agent did about it.

type TurnStarted struct {
	fact
	Turn   ID
	N      int // 1-based, what the human sees and /rollback takes
	Prompt string
}

func (TurnStarted) Kind() Kind { return TurnStartedKind }

// CheckpointTaken is the Turn's one snapshot, taken before any Call
// runs. It is the only thing a rollback restores to.
type CheckpointTaken struct {
	fact
	Turn     ID
	Snapshot string // container; "" when unsandboxed
	Tree     string // the human's own working directory
}

func (CheckpointTaken) Kind() Kind { return CheckpointTakenKind }

type TurnEnded struct {
	fact
	Turn    ID
	Reason  EndReason
	Summary string
	Usage   Usage // the whole Turn's cost
}

func (TurnEnded) Kind() Kind { return TurnEndedKind }

// EndReason is how a Turn stopped. Declined is absent on purpose:
// declining stops a Call, not a Turn.
type EndReason string

const (
	EndDone    EndReason = "done"    // the model stopped asking for tools
	EndStopped EndReason = "stopped" // a RequestStop was honoured
	EndAborted EndReason = "aborted" // the human said stop
	EndBound   EndReason = "bound"   // the human declined to continue
	EndError   EndReason = "error"
)

// BoundReached says the Turn hit MaxSteps and is waiting for Continue.
// Not an ending: the Turn is paused, and a human decides.
type BoundReached struct {
	fact
	Turn  ID
	Steps int
	Calls int
}

func (BoundReached) Kind() Kind { return BoundReachedKind }

type RolledBack struct {
	fact
	Turn        ID
	RevertFiles bool
}

func (RolledBack) Kind() Kind { return RolledBackKind }

// A Step is one model round trip, and the transcript's atom.

type StepStarted struct {
	fact
	Turn, Step ID
	N          int
}

func (StepStarted) Kind() Kind { return StepStartedKind }

type StepEnded struct {
	fact
	Turn, Step ID
	Usage      Usage
	Calls      int // how many the model asked for; 0 means it stopped
}

func (StepEnded) Kind() Kind { return StepEndedKind }

// ModelText is the model's own prose for this Step. Drawn as a row of
// its own, and judged like any other output.
type ModelText struct {
	fact
	Turn, Step ID
	Text       string
}

func (ModelText) Kind() Kind { return ModelTextKind }

// A Call is one tool invocation.

type CallProposed struct {
	fact
	Call, Step ID
	Tool       string
	Args       map[string]any
	Rationale  string
}

func (CallProposed) Kind() Kind { return CallProposedKind }

type CallAssessed struct {
	fact
	Call ID
	Risk Risk
}

func (CallAssessed) Kind() Kind { return CallAssessedKind }

// ApprovalAsked is the engine blocking on a human. Whoever answers
// publishes ResolveApproval with the same Call.
type ApprovalAsked struct {
	fact
	Call      ID
	Tool      string
	Args      map[string]any
	Rationale string
	Risk      Risk
}

func (ApprovalAsked) Kind() Kind { return ApprovalAskedKind }

type CallStarted struct {
	fact
	Call   ID
	Runner string // host or sandbox
}

func (CallStarted) Kind() Kind { return CallStartedKind }

// OutputChunk is one line of live output, and the only lossy event:
// a dropped line costs a redraw, where a dropped CallEnded is a row
// that never finishes. Parallel Calls interleave, so a consumer must
// route by Call rather than assume one is running.
type OutputChunk struct {
	fact
	Call   ID
	Line   string
	Stderr bool
}

func (OutputChunk) Kind() Kind  { return OutputChunkKind }
func (OutputChunk) Lossy() bool { return true }

type CallEnded struct {
	fact
	Call   ID
	Result Result
	Took   time.Duration
}

func (CallEnded) Kind() Kind { return CallEndedKind }

// Result is what a Call produced. A small mirror of capture.Result,
// which lives under internal/ and so cannot be imported here; the
// engine converts once.
type Result struct {
	ExitCode  int
	Stdout    string
	Stderr    string
	Truncated bool
	// Err is set when the tool could not run at all, as opposed to
	// running and failing.
	Err string
}

// CallJudged is the post-execution read: how it went and how to draw
// it. Async, so it may arrive well after CallEnded.
type CallJudged struct {
	fact
	Call         ID
	Status       string
	RenderKind   string
	Attention    float64
	GoalAchieved float64
	FromJudge    bool
}

func (CallJudged) Kind() Kind { return CallJudgedKind }

// ViewReady is a composed or looked-up spec for a Call's output.
type ViewReady struct {
	fact
	Call   ID
	Spec   *viewspec.Spec
	Source string // shipped, saved, composed
}

func (ViewReady) Kind() Kind { return ViewReadyKind }

// Notice is anything worth telling the human that is not about a
// specific Call.
type Notice struct {
	fact
	Level string // info, warn, error
	Text  string
}

func (Notice) Kind() Kind { return NoticeKind }

// SessionStarted is published once, and is where the welcome and
// status panes get their facts.
type SessionStarted struct {
	fact
	Session  ID
	Model    string
	Sandbox  bool
	Network  bool
	MaxSteps int
}

func (SessionStarted) Kind() Kind { return SessionStartedKind }
