// Package event is the vocabulary the engine and its front-ends share:
// facts about what happened, intents about what someone wants, and the
// Bus that carries both. Like viewspec it sits outside internal/ and
// depends on almost nothing, because both sides import it directly —
// that is what removes the translation layer between them.
package event

import "time"

// Event is anything the Bus carries.
type Event interface {
	Kind() Kind
	// Lossy reports whether a lagging subscriber may drop this rather
	// than let its queue grow. Live output may; nothing else may.
	Lossy() bool
}

// Record is one published Event plus what the Bus stamped on it. Seq
// is per-bus and gapless, so a lossy subscriber can tell it missed
// something and a persisted log replays in order.
type Record struct {
	Seq   uint64
	At    time.Time
	Event Event
}

// Kind names an Event. A closed set: it is the key a log query, a UI
// reducer and a Filter all switch on, and free text matches nothing
// reliably.
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

// IsIntent reports whether k is something someone wants rather than
// something that happened. The engine subscribes to one side, the UI
// to the other.
func (k Kind) IsIntent() bool { return intents[k] }

var intents = map[Kind]bool{
	SubmitPromptKind: true, ResolveApprovalKind: true, NoteContextKind: true,
	AbortKind: true, RequestStopKind: true, ContinueKind: true,
	RequestRollbackKind: true, ResetSessionKind: true,
}

// fact is embedded by every event that is not lossy, which is all but
// one of them.
type fact struct{}

func (fact) Lossy() bool { return false }
