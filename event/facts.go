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
	Session  uuid.UUID `json:"Session"`
	Model    string    `json:"Model"`
	Judge    string    `json:"Judge"` // "" when no classifier is wired
	Sandbox  bool      `json:"Sandbox"`
	Network  bool      `json:"Network"`
	MaxSteps int       `json:"MaxSteps"`
	// Recorded is false when nothing is writing this session down, so
	// a front-end can say it will not be resumable.
	Recorded bool `json:"Recorded"`
	// Resumed is how many stored records this run began from.
	Resumed int `json:"Resumed"`
	// ContextTokens is the transcript budget, which a Step's
	// PromptTokens is measured against. Zero means nobody said.
	ContextTokens int `json:"ContextTokens"`
	// Instructions are the project files the system prompt carries.
	Instructions []string `json:"Instructions"`
	// Skills are what the skill tool can load and the human can ask for.
	Skills []SkillSummary `json:"Skills"`
}

// SkillSummary is one skill found at startup.
type SkillSummary struct {
	Name        string `json:"Name"`
	Description string `json:"Description"`
	// Project is false for one from the human's home directory.
	Project bool `json:"Project"`
	// UserInvocable is false for a skill only the model may load.
	UserInvocable bool `json:"UserInvocable"`
}

func (SessionStarted) Kind() Kind { return SessionStartedKind }

// SessionResumed marks where a stored session was picked up again.
// Not a count on SessionStarted: two resumes are two seams.
type SessionResumed struct {
	fact
	Session uuid.UUID `json:"Session"`
	// Records is how many were replayed to get here.
	Records int `json:"Records"`
	// Sandbox is this run's mode, carried so an old seam still says it
	// once later runs have published their own.
	Sandbox bool `json:"Sandbox"`
}

func (SessionResumed) Kind() Kind { return SessionResumedKind }

// A Turn is one prompt and everything the agent did about it.

// TurnStarted opens a Turn.
type TurnStarted struct {
	fact
	Turn   uuid.UUID `json:"Turn"`
	N      int       `json:"N"` // 1-based, what the human sees and /rollback takes
	Prompt string    `json:"Prompt"`
}

func (TurnStarted) Kind() Kind { return TurnStartedKind }

// CheckpointTaken is the Turn's one snapshot, and all a rollback restores.
type CheckpointTaken struct {
	fact
	Turn     uuid.UUID `json:"Turn"`
	Snapshot string    `json:"Snapshot"` // container, "" when unsandboxed
	Tree     string    `json:"Tree"`     // the human's own working directory
}

func (CheckpointTaken) Kind() Kind { return CheckpointTakenKind }

// TurnEnded closes a Turn, however it stopped.
type TurnEnded struct {
	fact
	Turn    uuid.UUID `json:"Turn"`
	Reason  EndReason `json:"Reason"`
	Summary string    `json:"Summary"`
	Usage   Usage     `json:"Usage"` // the whole Turn's cost
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
	Turn      uuid.UUID `json:"Turn"`
	Steps     int       `json:"Steps"`
	ToolCalls int       `json:"ToolCalls"`
}

func (BoundReached) Kind() Kind { return BoundReachedKind }

// RolledBack says a Turn's checkpoint was restored.
type RolledBack struct {
	fact
	Turn        uuid.UUID `json:"Turn"`
	RevertFiles bool      `json:"RevertFiles"`
}

func (RolledBack) Kind() Kind { return RolledBackKind }

// SessionReset says the transcript was cleared, so a replay clears it too.
type SessionReset struct{ fact }

func (SessionReset) Kind() Kind { return SessionResetKind }

// A Step is one model round trip, and the transcript's atom.

// StepStarted opens a Step.
type StepStarted struct {
	fact
	Turn uuid.UUID `json:"Turn"`
	Step uuid.UUID `json:"Step"`
	N    int       `json:"N"`
}

func (StepStarted) Kind() Kind { return StepStartedKind }

// StepEnded closes a Step with what it cost.
type StepEnded struct {
	fact
	Turn      uuid.UUID `json:"Turn"`
	Step      uuid.UUID `json:"Step"`
	Usage     Usage     `json:"Usage"`
	ToolCalls int       `json:"ToolCalls"` // how many the model asked for, 0 when it stopped
	Stop      string    `json:"Stop"`      // the endpoint's reason the reply ended
}

func (StepEnded) Kind() Kind { return StepEndedKind }

// ModelText is the model's prose: its own row, judged like output.
type ModelText struct {
	fact
	Turn uuid.UUID `json:"Turn"`
	Step uuid.UUID `json:"Step"`
	Text string    `json:"Text"`
}

func (ModelText) Kind() Kind { return ModelTextKind }

// Appended is what went into the transcript, verbatim. Rebuilding it
// from CallEnded would mean reproducing formatResult forever.
type Appended struct {
	fact
	Turn     uuid.UUID `json:"Turn"`
	Step     uuid.UUID `json:"Step"`
	Messages []Message `json:"Messages"`
}

func (Appended) Kind() Kind { return AppendedKind }

// Compacted says the front of the transcript was replaced by one note.
// Dropped counts the messages that went.
type Compacted struct {
	fact
	Turn    uuid.UUID `json:"Turn"`
	Dropped int       `json:"Dropped"`
	Note    string    `json:"Note"`
}

func (Compacted) Kind() Kind { return CompactedKind }

// ContextMeasured is what the model's request is made of: what every Step
// resends, and the history compaction folds, oldest first.
type ContextMeasured struct {
	fact
	// Budget is where history compacts. Total is the endpoint's own count
	// when Exact, and otherwise an estimate scaled by the last one.
	Budget  int           `json:"Budget"`
	Total   int           `json:"Total"`
	Exact   bool          `json:"Exact"`
	Fixed   []ContextPart `json:"Fixed"`
	History []ContextPart `json:"History"`
	// Growth is how much history an average recent Step added.
	Growth int `json:"Growth"`
}

func (ContextMeasured) Kind() Kind { return MeasuredKind }

// ContextPart is one slice of a request, in tokens.
type ContextPart struct {
	Name   string `json:"Name"`
	Detail string `json:"Detail"`
	Tokens int    `json:"Tokens"`
	// N is the request a history part belongs to, 0 for the summary or notes.
	N int `json:"N"`
	// Open is the request still running, which compaction will not touch.
	Open bool `json:"Open"`
	// Largest is a history part's biggest message, a command that printed it or a reply.
	Largest       string `json:"Largest"`
	LargestTokens int    `json:"LargestTokens"`
}

// A tool call is one tool invocation.

// ToolCallProposed is a tool call the model asked for, before anything assesses it.
type ToolCallProposed struct {
	fact
	ToolCall  uuid.UUID      `json:"ToolCall"`
	Step      uuid.UUID      `json:"Step"`
	Tool      string         `json:"Tool"`
	Args      map[string]any `json:"Args"`
	Rationale string         `json:"Rationale"`
	// Renders is how the tool says its output should be read, which beats
	// a judged guess.
	Renders RenderKind `json:"Renders"`
	// Executor is empty for a shell command, and otherwise names what
	// runs it. Nothing a checkpoint can undo.
	Executor string `json:"Executor"`
}

func (ToolCallProposed) Kind() Kind { return ToolCallProposedKind }

// ToolCallAssessed is the hook chain's verdict on a tool call.
type ToolCallAssessed struct {
	fact
	ToolCall uuid.UUID `json:"ToolCall"`
	Risk     Risk      `json:"Risk"`
}

func (ToolCallAssessed) Kind() Kind { return ToolCallAssessedKind }

// ApprovalAsked blocks the engine until a ResolveApproval names this tool call.
type ApprovalAsked struct {
	fact
	ToolCall  uuid.UUID      `json:"ToolCall"`
	Tool      string         `json:"Tool"`
	Args      map[string]any `json:"Args"`
	Rationale string         `json:"Rationale"`
	Risk      Risk           `json:"Risk"`
}

func (ApprovalAsked) Kind() Kind { return ApprovalAskedKind }

// ToolCallStarted says a tool call began running.
type ToolCallStarted struct {
	fact
	ToolCall uuid.UUID `json:"ToolCall"`
	Runner   string    `json:"Runner"` // host or sandbox
}

func (ToolCallStarted) Kind() Kind { return ToolCallStartedKind }

// OutputChunk is one live line, and the only lossy event. Parallel
// Tool calls interleave, so route by id rather than assume one is running.
type OutputChunk struct {
	// Exactly one is set: a tool call the model asked for, or a user command.
	ToolCall    uuid.UUID `json:"ToolCall"`
	UserCommand uuid.UUID `json:"UserCommand"`
	Line        string    `json:"Line"`
	Stderr      bool      `json:"Stderr"`
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
	ToolCall uuid.UUID     `json:"ToolCall"`
	Result   Result        `json:"Result"`
	Took     time.Duration `json:"Took"`
}

func (ToolCallEnded) Kind() Kind { return ToolCallEndedKind }

// Result mirrors capture.Result, which is under internal/ and so
// unreachable from here. The engine converts once.
type Result struct {
	ExitCode  int    `json:"ExitCode"`
	Stdout    string `json:"Stdout"`
	Stderr    string `json:"Stderr"`
	Truncated bool   `json:"Truncated"`
	// Err is set when the tool could not run, not when it ran and failed.
	Err string `json:"Err"`
}

// ToolCallJudged is how it went and how to draw it. Async: may land late.
type ToolCallJudged struct {
	fact
	ToolCall     uuid.UUID  `json:"ToolCall"`
	Status       Status     `json:"Status"`
	RenderKind   RenderKind `json:"RenderKind"`
	Attention    float64    `json:"Attention"`
	GoalAchieved float64    `json:"GoalAchieved"`
	FromJudge    bool       `json:"FromJudge"`
}

func (ToolCallJudged) Kind() Kind { return ToolCallJudgedKind }

// ViewReady is a spec for a tool call's output.
type ViewReady struct {
	fact
	// Exactly one is set, as on OutputChunk.
	ToolCall    uuid.UUID      `json:"ToolCall"`
	UserCommand uuid.UUID      `json:"UserCommand"`
	Spec        *viewspec.Spec `json:"Spec"`
	Source      string         `json:"Source"` // shipped, saved, composed
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
	UserCommand uuid.UUID `json:"UserCommand"`
	Command     string    `json:"Command"`
	Runner      string    `json:"Runner"` // host or sandbox
}

func (UserCommandStarted) Kind() Kind { return UserCommandStartedKind }

// UserCommandEnded is a user command's whole result.
type UserCommandEnded struct {
	fact
	UserCommand uuid.UUID     `json:"UserCommand"`
	Result      Result        `json:"Result"`
	Took        time.Duration `json:"Took"`
}

func (UserCommandEnded) Kind() Kind { return UserCommandEndedKind }

// SessionsListed answers ListSessions with what can be resumed.
type SessionsListed struct {
	fact
	Sessions []SessionSummary `json:"Sessions"`
}

func (SessionsListed) Kind() Kind { return SessionsListedKind }

// SessionSummary is one resumable session, as a listing shows it.
// Started is UTC, since a time crossing the wire keeps only its instant.
type SessionSummary struct {
	ID uuid.UUID `json:"ID"`
	// Name is what a human called it, "" until they do.
	Name    string    `json:"Name"`
	Started time.Time `json:"Started"`
	Model   string    `json:"Model"`
	Events  int       `json:"Events"`
}

// ServersListed answers ListServers, failures included: a missing
// server is the thing a human needs told.
type ServersListed struct {
	fact
	Servers []ServerSummary `json:"Servers"`
}

func (ServersListed) Kind() Kind { return ServersListedKind }

// ServerSummary is one MCP server as configured, connected or not.
type ServerSummary struct {
	Name    string `json:"Name"`
	Command string `json:"Command"`
	// Connected tells a server still being dialled from one that
	// answered and offers nothing, which look alike from Tools alone.
	Connected bool `json:"Connected"`
	// Tools is how many it offered, once connected.
	Tools int `json:"Tools"`
	// Err is why it is not connected, "" when it is.
	Err string `json:"Err"`
	// Disabled is a server left in the config but switched off.
	Disabled bool `json:"Disabled"`
	// Auth is "" for a server with no auth, else one of the Auth values.
	Auth string `json:"Auth"`
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
	Server string    `json:"Server"`
	URL    string    `json:"URL"`
	Until  time.Time `json:"Until"`
}

func (AuthorizationWaiting) Kind() Kind { return AuthorizationWaitingKind }

// ServerAuthorized says a sign-in worked: a token was got and saved.
type ServerAuthorized struct {
	fact
	Server string `json:"Server"`
}

func (ServerAuthorized) Kind() Kind { return ServerAuthorizedKind }

// AuthorizationFailed says a sign-in ended with no token: it expired,
// was stopped, or was refused. Reason is for a human.
type AuthorizationFailed struct {
	fact
	Server string `json:"Server"`
	Reason string `json:"Reason"`
}

func (AuthorizationFailed) Kind() Kind { return AuthorizationFailedKind }

// Notice is anything to say that is not about one tool call.
type Notice struct {
	fact
	Level string `json:"Level"` // info, warn, error
	Text  string `json:"Text"`
}

func (Notice) Kind() Kind { return NoticeKind }
