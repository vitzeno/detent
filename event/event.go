// Package event is the shared vocabulary: facts about what happened,
// intents about what someone wants, and the Bus that carries both.
// Both the engine and ui import it, so it depends on next to nothing.
package event

import "time"

// Event is anything the Bus carries.
type Event interface {
	Kind() Kind
	// Lossy: a lagging subscriber may drop this. Live output only.
	Lossy() bool
}

// Record is one Event plus what the Bus stamped on it. Ordinal counts
// publishes and is gapless, so a subscriber that filters or drops can
// be told apart from one that lost something.
type Record struct {
	Ordinal uint64
	At      time.Time
	Event   Event
}

// Kind names an Event. Closed: a log query and a reducer both key on it.
type Kind string

// Facts. Past tense, published by the engine.
const (
	SessionStartedKind Kind = "session.started"

	TurnStartedKind     Kind = "turn.started"
	TurnEndedKind       Kind = "turn.ended"
	CheckpointTakenKind Kind = "turn.checkpoint"
	RolledBackKind      Kind = "turn.rolledback"
	BoundReachedKind    Kind = "turn.bound"

	StepStartedKind Kind = "step.started"
	StepEndedKind   Kind = "step.ended"
	ModelTextKind   Kind = "step.text"

	// The transcript, which is derived rather than carried by the
	// facts above, so replay needs its own record of it.
	AppendedKind  Kind = "transcript.appended"
	CompactedKind Kind = "transcript.compacted"

	CallProposedKind  Kind = "call.proposed"
	CallAssessedKind  Kind = "call.assessed"
	ApprovalAskedKind Kind = "call.approval"
	CallStartedKind   Kind = "call.started"
	OutputChunkKind   Kind = "call.output"
	CallEndedKind     Kind = "call.ended"
	CallJudgedKind    Kind = "call.judged"
	ViewReadyKind     Kind = "call.view"

	NoticeKind Kind = "notice"
)

// Intents. Imperative, published by anyone.
const (
	SubmitPromptKind    Kind = "do.prompt"
	ResolveApprovalKind Kind = "do.approve"
	NoteContextKind     Kind = "do.note"
	AbortKind           Kind = "do.abort"
	RequestStopKind     Kind = "do.stop"
	ContinueKind        Kind = "do.continue"
	RequestRollbackKind Kind = "do.rollback"
	ResetSessionKind    Kind = "do.reset"
)

// IsIntent splits what someone wants from what happened. The engine
// subscribes to one side, a front-end to the other.
func (k Kind) IsIntent() bool { return intents[k] }

var intents = map[Kind]bool{
	SubmitPromptKind: true, ResolveApprovalKind: true, NoteContextKind: true,
	AbortKind: true, RequestStopKind: true, ContinueKind: true,
	RequestRollbackKind: true, ResetSessionKind: true,
}

// fact is embedded by everything but OutputChunk.
type fact struct{}

func (fact) Lossy() bool { return false }
