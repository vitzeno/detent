package event

import (
	"github.com/google/uuid"
	"time"

	"github.com/vitzeno/detent/viewspec"
)

// What the engine publishes, past tense. ApprovalAsked is the one
// question among them, correlated by Call.

// SessionStarted is published once; welcome, status and the log all
// read it, so it is the one description of a run.
type SessionStarted struct {
	fact
	Session  uuid.UUID
	Model    string
	Judge    string // "" when no classifier is wired
	Sandbox  bool
	Network  bool
	MaxSteps int
	// Recorded is false when nothing is writing this session down, so
	// a front-end can say it will not be resumable.
	Recorded bool
	// Resumed is how many stored records this run began from.
	Resumed int
}

func (SessionStarted) Kind() Kind { return SessionStartedKind }

// A Turn is one prompt and everything the agent did about it.

type TurnStarted struct {
	fact
	Turn   uuid.UUID
	N      int // 1-based, what the human sees and /rollback takes
	Prompt string
}

func (TurnStarted) Kind() Kind { return TurnStartedKind }

// CheckpointTaken is the Turn's one snapshot, and all a rollback restores.
type CheckpointTaken struct {
	fact
	Turn     uuid.UUID
	Snapshot string // container; "" when unsandboxed
	Tree     string // the human's own working directory
}

func (CheckpointTaken) Kind() Kind { return CheckpointTakenKind }

type TurnEnded struct {
	fact
	Turn    uuid.UUID
	Reason  EndReason
	Summary string
	Usage   Usage // the whole Turn's cost
}

func (TurnEnded) Kind() Kind { return TurnEndedKind }

// EndReason is how a Turn stopped. No Declined: that stops a Call.
type EndReason string

const (
	EndDone    EndReason = "done"    // the model stopped asking for tools
	EndStopped EndReason = "stopped" // a RequestStop was honoured
	EndAborted EndReason = "aborted" // the human said stop
	EndBound   EndReason = "bound"   // the human declined to continue
	EndError   EndReason = "error"
)

// BoundReached pauses the Turn at MaxSteps to ask. Not an ending.
type BoundReached struct {
	fact
	Turn  uuid.UUID
	Steps int
	Calls int
}

func (BoundReached) Kind() Kind { return BoundReachedKind }

type RolledBack struct {
	fact
	Turn        uuid.UUID
	RevertFiles bool
}

func (RolledBack) Kind() Kind { return RolledBackKind }

// A Step is one model round trip, and the transcript's atom.

type StepStarted struct {
	fact
	Turn, Step uuid.UUID
	N          int
}

func (StepStarted) Kind() Kind { return StepStartedKind }

type StepEnded struct {
	fact
	Turn, Step uuid.UUID
	Usage      Usage
	Calls      int // how many the model asked for; 0 means it stopped
}

func (StepEnded) Kind() Kind { return StepEndedKind }

// ModelText is the model's prose: its own row, judged like output.
type ModelText struct {
	fact
	Turn, Step uuid.UUID
	Text       string
}

func (ModelText) Kind() Kind { return ModelTextKind }

// Appended is what went into the transcript, verbatim. Rebuilding it
// from CallEnded would mean reproducing formatResult forever.
type Appended struct {
	fact
	Turn, Step uuid.UUID
	Messages   []Message
}

func (Appended) Kind() Kind { return AppendedKind }

// Compacted says the front of the transcript was replaced by one note.
// Dropped counts the messages that went.
type Compacted struct {
	fact
	Turn    uuid.UUID
	Dropped int
	Note    string
}

func (Compacted) Kind() Kind { return CompactedKind }

// A Call is one tool invocation.

type CallProposed struct {
	fact
	Call, Step uuid.UUID
	Tool       string
	Args       map[string]any
	Rationale  string
}

func (CallProposed) Kind() Kind { return CallProposedKind }

type CallAssessed struct {
	fact
	Call uuid.UUID
	Risk Risk
}

func (CallAssessed) Kind() Kind { return CallAssessedKind }

// ApprovalAsked blocks the engine until a ResolveApproval names this Call.
type ApprovalAsked struct {
	fact
	Call      uuid.UUID
	Tool      string
	Args      map[string]any
	Rationale string
	Risk      Risk
}

func (ApprovalAsked) Kind() Kind { return ApprovalAskedKind }

type CallStarted struct {
	fact
	Call   uuid.UUID
	Runner string // host or sandbox
}

func (CallStarted) Kind() Kind { return CallStartedKind }

// OutputChunk is one live line, and the only lossy event. Parallel
// Calls interleave, so route by Call rather than assume one is running.
type OutputChunk struct {
	fact
	Call   uuid.UUID
	Line   string
	Stderr bool
}

func (OutputChunk) Kind() Kind  { return OutputChunkKind }
func (OutputChunk) Lossy() bool { return true }

type CallEnded struct {
	fact
	Call   uuid.UUID
	Result Result
	Took   time.Duration
}

func (CallEnded) Kind() Kind { return CallEndedKind }

// Result mirrors capture.Result, which is under internal/ and so
// unreachable from here. The engine converts once.
type Result struct {
	ExitCode  int
	Stdout    string
	Stderr    string
	Truncated bool
	// Err is set when the tool could not run, not when it ran and failed.
	Err string
}

// CallJudged is how it went and how to draw it. Async: may land late.
type CallJudged struct {
	fact
	Call         uuid.UUID
	Status       string
	RenderKind   string
	Attention    float64
	GoalAchieved float64
	FromJudge    bool
}

func (CallJudged) Kind() Kind { return CallJudgedKind }

// ViewReady is a spec for a Call's output.
type ViewReady struct {
	fact
	Call   uuid.UUID
	Spec   *viewspec.Spec
	Source string // shipped, saved, composed
}

func (ViewReady) Kind() Kind { return ViewReadyKind }

// SessionsListed answers ListSessions with what can be resumed.
type SessionsListed struct {
	fact
	Sessions []SessionSummary
}

func (SessionsListed) Kind() Kind { return SessionsListedKind }

// SessionSummary is one resumable session, as a listing shows it.
// Started is UTC: a time crossing the wire keeps its instant and not
// its zone.
type SessionSummary struct {
	ID uuid.UUID
	// Name is what a human called it, "" until they do.
	Name    string
	Started time.Time
	Model   string
	Events  int
}

// Notice is anything to say that is not about one Call.
type Notice struct {
	fact
	Level string // info, warn, error
	Text  string
}

func (Notice) Kind() Kind { return NoticeKind }
