package event

import "github.com/google/uuid"

// What anyone publishes, imperative. The engine subscribes to these
// and nothing else.

// SubmitPrompt opens a Turn. Typed mid-Turn it becomes a NoteContext.
type SubmitPrompt struct {
	fact
	Text string
}

func (SubmitPrompt) Kind() Kind { return SubmitPromptKind }

// ResolveApproval answers one ApprovalAsked. The engine does not care
// whether a human, an auto-approver or a policy sent it.
type ResolveApproval struct {
	fact
	Call     uuid.UUID
	Approved bool
}

func (ResolveApproval) Kind() Kind { return ResolveApprovalKind }

// NoteContext puts a message in the transcript with no tool run: how a
// human steers mid-Turn. Flushed at a Step boundary, never inside one.
type NoteContext struct {
	fact
	Text string
}

func (NoteContext) Kind() Kind { return NoteContextKind }

// Abort cancels in-flight Calls and ends the Turn. The Step still
// completes, with a result per unrun Call, or the next Step fails.
type Abort struct {
	fact
	Turn uuid.UUID
}

func (Abort) Kind() Kind { return AbortKind }

// RequestStop is advisory, honoured at the next Step boundary. How the
// post-execution judge acts without becoming an interceptor.
type RequestStop struct {
	fact
	Turn   uuid.UUID
	Reason string
}

func (RequestStop) Kind() Kind { return RequestStopKind }

// Continue answers BoundReached; false ends the Turn.
type Continue struct {
	fact
	Turn     uuid.UUID
	Approved bool
}

func (Continue) Kind() Kind { return ContinueKind }

// RequestRollback restores the Turn's checkpoint and truncates the
// transcript to where it began. RevertFiles also reverts the workspace.
type RequestRollback struct {
	fact
	Turn        uuid.UUID
	RevertFiles bool
}

func (RequestRollback) Kind() Kind { return RequestRollbackKind }

// ListSessions asks what can be resumed. Whatever holds the log
// answers with SessionsListed.
type ListSessions struct{ fact }

func (ListSessions) Kind() Kind { return ListSessionsKind }

// RenameSession gives a session a name a human will recognise. Not a
// fact about what happened, so it is not in the log: the name is the
// header's own, and whatever holds it answers with a fresh listing.
type RenameSession struct {
	fact
	Session uuid.UUID
	Name    string
}

func (RenameSession) Kind() Kind { return RenameSessionKind }

// ResetSession forgets the transcript. The container keeps running.
type ResetSession struct{ fact }

func (ResetSession) Kind() Kind { return ResetSessionKind }
