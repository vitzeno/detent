// Package event is the shared vocabulary: facts about what happened,
// intents about what someone wants, and the Bus that carries both.
// Both the engine and ui import it, so it depends on next to nothing.
package event

import "time"

// Event is anything the Bus carries. Every subscriber shares its maps and
// slices, so an Event is immutable once published: copy before changing.
type Event interface {
	Kind() Kind
	// Lossy: a lagging subscriber may drop this. Live output only.
	Lossy() bool
}

// Record is one Event plus what the Bus stamped on it. Ordinals are gapless
// and arrive in order, so an unfiltered subscriber can tell one was lost.
type Record struct {
	Ordinal uint64
	At      time.Time
	Event   Event
}

// Kind names an Event. Closed: a log query and a reducer both key on it.
type Kind string

// Facts. Past tense, mostly published by the engine.
const (
	SessionStartedKind Kind = "session.started"
	SessionResumedKind Kind = "session.resumed"
	SessionResetKind   Kind = "session.reset"
	SessionsListedKind Kind = "sessions.listed"
	SessionLoadedKind  Kind = "session.loaded"
	NoticeKind         Kind = "notice"

	TurnStartedKind     Kind = "turn.started"
	CheckpointTakenKind Kind = "turn.checkpoint"
	TurnEndedKind       Kind = "turn.ended"
	BoundReachedKind    Kind = "turn.bound"
	RolledBackKind      Kind = "turn.rolledback"

	AgentStartedKind Kind = "agent.started"
	AgentEndedKind   Kind = "agent.ended"

	StepStartedKind Kind = "step.started"
	StepEndedKind   Kind = "step.ended"
	ModelTextKind   Kind = "step.text"

	AppendedKind  Kind = "transcript.appended"
	CompactedKind Kind = "transcript.compacted"
	MeasuredKind  Kind = "transcript.measured"

	ToolCallProposedKind Kind = "tool_call.proposed"
	ToolCallAssessedKind Kind = "tool_call.assessed"
	ApprovalAskedKind    Kind = "tool_call.approval"
	ToolCallStartedKind  Kind = "tool_call.started"
	OutputChunkKind      Kind = "output.chunk"
	ToolCallEndedKind    Kind = "tool_call.ended"
	ToolCallJudgedKind   Kind = "tool_call.judged"
	ViewReadyKind        Kind = "view.ready"

	UserCommandStartedKind Kind = "user_command.started"
	UserCommandEndedKind   Kind = "user_command.ended"

	ServersListedKind        Kind = "servers.listed"
	AuthorizationWaitingKind Kind = "auth.waiting"
	ServerAuthorizedKind     Kind = "auth.done"
	AuthorizationFailedKind  Kind = "auth.failed"
)

// Intents. Imperative, published by anyone, each owned by one subscriber.
const (
	// The engine's.
	SubmitPromptKind    Kind = "do.prompt"
	NoteContextKind     Kind = "do.note"
	ResolveApprovalKind Kind = "do.approve"
	AbortKind           Kind = "do.abort"
	SuggestFinishKind   Kind = "do.suggest_finish"
	ContinueKind        Kind = "do.continue"
	RequestRollbackKind Kind = "do.rollback"
	ResetSessionKind    Kind = "do.reset"
	MeasureContextKind  Kind = "do.measure_context"
	ResumeSessionKind   Kind = "do.resume_session"

	// The store's.
	ListSessionsKind  Kind = "do.list_sessions"
	RenameSessionKind Kind = "do.rename_session"
	LoadSessionKind   Kind = "do.load_session"

	// forget's.
	DeleteSessionKind Kind = "do.delete_session"

	// usercommand's.
	RunCommandKind    Kind = "do.run_command"
	CancelCommandKind Kind = "do.cancel_command"
	StopAgentKind     Kind = "do.stop_agent"

	// mcp's.
	ListServersKind     Kind = "do.list_servers"
	AuthorizeServerKind Kind = "do.authorize"
	OpenAuthKind        Kind = "do.open_authorization"
)

// IsIntent splits what someone wants from what happened. The engine
// subscribes to one side, a front-end to the other.
func (k Kind) IsIntent() bool { return intents[k] }

var intents = map[Kind]bool{
	SubmitPromptKind: true, NoteContextKind: true, ResolveApprovalKind: true,
	AbortKind: true, SuggestFinishKind: true, ContinueKind: true,
	RequestRollbackKind: true, ResetSessionKind: true, MeasureContextKind: true,
	ResumeSessionKind: true,
	ListSessionsKind:  true, RenameSessionKind: true, LoadSessionKind: true, DeleteSessionKind: true,
	RunCommandKind: true, CancelCommandKind: true, StopAgentKind: true,
	ListServersKind: true, AuthorizeServerKind: true, OpenAuthKind: true,
}

// fact marks an event as never dropped. Every event but OutputChunk
// embeds it, intents included.
type fact struct{}

func (fact) Lossy() bool { return false }
