package event

// What anyone publishes. Imperative: each says someone wants something
// to happen. The engine subscribes to these and to nothing else.

// SubmitPrompt opens a Turn. Typed while one is running, it becomes a
// NoteContext instead — steering, not a new Turn.
type SubmitPrompt struct {
	fact
	Text string
}

func (SubmitPrompt) Kind() Kind { return SubmitPromptKind }

// ResolveApproval answers one ApprovalAsked. Correlated by Call, so
// the engine does not care whether a human, an auto-approver or a
// policy answered.
type ResolveApproval struct {
	fact
	Call     ID
	Approved bool
}

func (ResolveApproval) Kind() Kind { return ResolveApprovalKind }

// NoteContext puts a message in the transcript with no tool run. It is
// how a human steers mid-Turn, and how a skill injects instruction.
// Queued and flushed at a Step boundary, never inside one.
type NoteContext struct {
	fact
	Text string
}

func (NoteContext) Kind() Kind { return NoteContextKind }

// Abort stops now: in-flight Calls are cancelled and the Turn ends.
// The Step is still completed, with a result for every Call that never
// ran, or the transcript is malformed for the next Turn.
type Abort struct {
	fact
	Turn ID
}

func (Abort) Kind() Kind { return AbortKind }

// RequestStop is advisory. The engine honours it at the next Step
// boundary, never inside one. This is how the post-execution judge
// acts on its own verdict without becoming an interceptor.
type RequestStop struct {
	fact
	Turn   ID
	Reason string
}

func (RequestStop) Kind() Kind { return RequestStopKind }

// Continue answers BoundReached. Approved false ends the Turn.
type Continue struct {
	fact
	Turn     ID
	Approved bool
}

func (Continue) Kind() Kind { return ContinueKind }

// RequestRollback restores the Turn's checkpoint and truncates the
// transcript to where it began. RevertFiles also puts the human's own
// files back.
type RequestRollback struct {
	fact
	Turn        ID
	RevertFiles bool
}

func (RequestRollback) Kind() Kind { return RequestRollbackKind }

// ResetSession forgets the transcript and the Turns. The container
// keeps running: the conversation and the environment are different
// things.
type ResetSession struct{ fact }

func (ResetSession) Kind() Kind { return ResetSessionKind }
