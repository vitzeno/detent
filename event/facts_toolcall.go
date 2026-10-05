package event

import (
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/viewspec"
)

// A tool call is one tool invocation.

// ToolCallProposed is a tool call the model asked for, before anything assesses it.
type ToolCallProposed struct {
	fact
	Step      uuid.UUID      `json:"Step"`
	ToolCall  uuid.UUID      `json:"ToolCall"`
	Tool      string         `json:"Tool"`
	Args      map[string]any `json:"Args"`
	Rationale string         `json:"Rationale"`
	// Renders is how the tool says its output should be read, which beats
	// a judged guess.
	Renders RenderKind `json:"Renders"`
	// Executor is empty for a shell command, and otherwise names what
	// runs it. Nothing a checkpoint can undo.
	Executor string `json:"Executor"`
	// Agent is the subagent this came from, uuid.Nil for the root.
	Agent uuid.UUID `json:"Agent"`
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
	// Agent is the subagent this came from, uuid.Nil for the root.
	Agent uuid.UUID `json:"Agent"`
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
// tool calls interleave, so route by id rather than assume one is running.
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
