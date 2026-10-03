package event

import (
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/viewspec"
)

// What happened, past tense. ApprovalAsked is the one question among
// them, correlated by tool call.

// SessionStarted is published once. Welcome, status and the log all
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
	// ContextTokens is the transcript budget, which a Step's
	// PromptTokens is measured against. Zero means nobody said.
	ContextTokens int
	// Instructions are the project files the system prompt carries.
	Instructions []string
	// Skills are what the skill tool can load and the human can ask for.
	Skills []SkillSummary
}

// SkillSummary is one skill found at startup.
type SkillSummary struct {
	Name        string
	Description string
	// Project is false for one from the human's home directory.
	Project bool
	// UserInvocable is false for a skill only the model may load.
	UserInvocable bool
}

func (SessionStarted) Kind() Kind { return SessionStartedKind }

// SessionResumed marks where a stored session was picked up again.
// Not a count on SessionStarted: two resumes are two seams.
type SessionResumed struct {
	fact
	Session uuid.UUID
	// Records is how many were replayed to get here.
	Records int
	// Sandbox is this run's mode, carried so an old seam still says it
	// once later runs have published their own.
	Sandbox bool
}

func (SessionResumed) Kind() Kind { return SessionResumedKind }

// A Turn is one prompt and everything the agent did about it.

// TurnStarted opens a Turn.
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
	Snapshot string // container, "" when unsandboxed
	Tree     string // the human's own working directory
}

func (CheckpointTaken) Kind() Kind { return CheckpointTakenKind }

// TurnEnded closes a Turn, however it stopped.
type TurnEnded struct {
	fact
	Turn    uuid.UUID
	Reason  EndReason
	Summary string
	Usage   Usage // the whole Turn's cost
}

func (TurnEnded) Kind() Kind { return TurnEndedKind }

// EndReason is how a Turn stopped. No Declined: that stops a tool call.
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
	Turn      uuid.UUID
	Steps     int
	ToolCalls int
}

func (BoundReached) Kind() Kind { return BoundReachedKind }

// RolledBack says a Turn's checkpoint was restored.
type RolledBack struct {
	fact
	Turn        uuid.UUID
	RevertFiles bool
}

func (RolledBack) Kind() Kind { return RolledBackKind }

// SessionReset says the transcript was cleared, so a replay clears it too.
type SessionReset struct{ fact }

func (SessionReset) Kind() Kind { return SessionResetKind }

// A Step is one model round trip, and the transcript's atom.

// StepStarted opens a Step.
type StepStarted struct {
	fact
	Turn, Step uuid.UUID
	N          int
}

func (StepStarted) Kind() Kind { return StepStartedKind }

// StepEnded closes a Step with what it cost.
type StepEnded struct {
	fact
	Turn, Step uuid.UUID
	Usage      Usage
	ToolCalls  int    // how many the model asked for, 0 when it stopped
	Stop       string // the endpoint's reason the reply ended
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

// ContextMeasured is what the model's request is made of: what every Step
// resends, and the history compaction folds, oldest first.
type ContextMeasured struct {
	fact
	// Budget is where history compacts. Total is the endpoint's own count
	// when Exact, and otherwise an estimate scaled by the last one.
	Budget  int
	Total   int
	Exact   bool
	Fixed   []ContextPart
	History []ContextPart
	// Growth is how much history an average recent Step added.
	Growth int
}

func (ContextMeasured) Kind() Kind { return MeasuredKind }

// ContextPart is one slice of a request, in tokens.
type ContextPart struct {
	Name   string
	Detail string
	Tokens int
	// N is the request a history part belongs to, 0 for the summary or notes.
	N int
	// Open is the request still running, which compaction will not touch.
	Open bool
	// Largest is a history part's biggest message, a command that printed it or a reply.
	Largest       string
	LargestTokens int
}

// A tool call is one tool invocation.

// ToolCallProposed is a tool call the model asked for, before anything assesses it.
type ToolCallProposed struct {
	fact
	ToolCall, Step uuid.UUID
	Tool           string
	Args           map[string]any
	Rationale      string
	// Renders is how the tool says its output should be read, which beats
	// a judged guess.
	Renders string
	// Executor is empty for a shell command, and otherwise names what
	// runs it. Nothing a checkpoint can undo.
	Executor string
}

func (ToolCallProposed) Kind() Kind { return ToolCallProposedKind }

// RendersMarkdown says a tool's output is a markdown document.
const RendersMarkdown = "markdown"

// RendersDiff says a tool's output is a unified diff, as an edit prints.
const RendersDiff = "diff"

// ToolCallAssessed is the hook chain's verdict on a tool call.
type ToolCallAssessed struct {
	fact
	ToolCall uuid.UUID
	Risk     Risk
}

func (ToolCallAssessed) Kind() Kind { return ToolCallAssessedKind }

// ApprovalAsked blocks the engine until a ResolveApproval names this tool call.
type ApprovalAsked struct {
	fact
	ToolCall  uuid.UUID
	Tool      string
	Args      map[string]any
	Rationale string
	Risk      Risk
}

func (ApprovalAsked) Kind() Kind { return ApprovalAskedKind }

// ToolCallStarted says a tool call began running.
type ToolCallStarted struct {
	fact
	ToolCall uuid.UUID
	Runner   string // host or sandbox
}

func (ToolCallStarted) Kind() Kind { return ToolCallStartedKind }

// OutputChunk is one live line, and the only lossy event. Parallel
// Tool calls interleave, so route by id rather than assume one is running.
type OutputChunk struct {
	// Exactly one is set: a tool call the model asked for, or a user command.
	ToolCall    uuid.UUID
	UserCommand uuid.UUID
	Line        string
	Stderr      bool
}

// Owner is whichever of tool call and user command printed the line.
func (o OutputChunk) Owner() uuid.UUID {
	if o.UserCommand != uuid.Nil {
		return o.UserCommand
	}
	return o.ToolCall
}

func (OutputChunk) Kind() Kind  { return OutputChunkKind }
func (OutputChunk) Lossy() bool { return true }

// ToolCallEnded is a tool call's whole result.
type ToolCallEnded struct {
	fact
	ToolCall uuid.UUID
	Result   Result
	Took     time.Duration
}

func (ToolCallEnded) Kind() Kind { return ToolCallEndedKind }

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

// ToolCallJudged is how it went and how to draw it. Async: may land late.
type ToolCallJudged struct {
	fact
	ToolCall     uuid.UUID
	Status       string
	RenderKind   string
	Attention    float64
	GoalAchieved float64
	FromJudge    bool
}

func (ToolCallJudged) Kind() Kind { return ToolCallJudgedKind }

// ViewReady is a spec for a tool call's output.
type ViewReady struct {
	fact
	// Exactly one is set, as on OutputChunk.
	ToolCall    uuid.UUID
	UserCommand uuid.UUID
	Spec        *viewspec.Spec
	Source      string // shipped, saved, composed
}

func (ViewReady) Kind() Kind { return ViewReadyKind }

// Owner is whichever of ToolCall and UserCommand the view is for.
func (v ViewReady) Owner() uuid.UUID {
	if v.UserCommand != uuid.Nil {
		return v.UserCommand
	}
	return v.ToolCall
}

// A user command is one command the human ran themselves. Not a tool call: the
// model never asked for it, so nothing assesses or approves it.

// UserCommandStarted says a user command began running.
type UserCommandStarted struct {
	fact
	UserCommand uuid.UUID
	Command     string
	Runner      string // host or sandbox
}

func (UserCommandStarted) Kind() Kind { return UserCommandStartedKind }

// UserCommandEnded is a user command's whole result.
type UserCommandEnded struct {
	fact
	UserCommand uuid.UUID
	Result      Result
	Took        time.Duration
}

func (UserCommandEnded) Kind() Kind { return UserCommandEndedKind }

// SessionsListed answers ListSessions with what can be resumed.
type SessionsListed struct {
	fact
	Sessions []SessionSummary
}

func (SessionsListed) Kind() Kind { return SessionsListedKind }

// SessionSummary is one resumable session, as a listing shows it.
// Started is UTC, since a time crossing the wire keeps only its instant.
type SessionSummary struct {
	ID uuid.UUID
	// Name is what a human called it, "" until they do.
	Name    string
	Started time.Time
	Model   string
	Events  int
}

// ServersListed answers ListServers, failures included: a missing
// server is the thing a human needs told.
type ServersListed struct {
	fact
	Servers []ServerSummary
}

func (ServersListed) Kind() Kind { return ServersListedKind }

// ServerSummary is one MCP server as configured, connected or not.
type ServerSummary struct {
	Name    string
	Command string
	// Connected tells a server still being dialled from one that
	// answered and offers nothing, which look alike from Tools alone.
	Connected bool
	// Tools is how many it offered, once connected.
	Tools int
	// Err is why it is not connected, "" when it is.
	Err string
	// Disabled is a server left in the config but switched off.
	Disabled bool
	// Auth is "" for a server with no auth, else one of the Auth values.
	Auth string
}

// Auth is where a server's sign-in stands, for one that has auth.
const (
	AuthSignedIn  = "signed in"
	AuthWaiting   = "waiting"
	AuthSignedOut = "signed out"
)

// AuthorizationWaiting says a server wants a human to sign in, at URL.
// Until is when the wait gives up. The URL holds no secret.
type AuthorizationWaiting struct {
	fact
	Server string
	URL    string
	Until  time.Time
}

func (AuthorizationWaiting) Kind() Kind { return AuthorizationWaitingKind }

// ServerAuthorized says a sign-in worked: a token was got and saved.
type ServerAuthorized struct {
	fact
	Server string
}

func (ServerAuthorized) Kind() Kind { return ServerAuthorizedKind }

// AuthorizationFailed says a sign-in ended with no token: it expired,
// was stopped, or was refused. Reason is for a human.
type AuthorizationFailed struct {
	fact
	Server string
	Reason string
}

func (AuthorizationFailed) Kind() Kind { return AuthorizationFailedKind }

// Notice is anything to say that is not about one tool call.
type Notice struct {
	fact
	Level string // info, warn, error
	Text  string
}

func (Notice) Kind() Kind { return NoticeKind }
