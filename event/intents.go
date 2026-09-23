package event

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
	Call     ID
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
	Turn ID
}

func (Abort) Kind() Kind { return AbortKind }

// RequestStop is advisory, honoured at the next Step boundary. How the
// post-execution judge acts without becoming an interceptor.
type RequestStop struct {
	fact
	Turn   ID
	Reason string
}

func (RequestStop) Kind() Kind { return RequestStopKind }

// Continue answers BoundReached; false ends the Turn.
type Continue struct {
	fact
	Turn     ID
	Approved bool
}

func (Continue) Kind() Kind { return ContinueKind }

// RequestRollback restores the Turn's checkpoint and truncates the
// transcript to where it began. RevertFiles also reverts the workspace.
type RequestRollback struct {
	fact
	Turn        ID
	RevertFiles bool
}

func (RequestRollback) Kind() Kind { return RequestRollbackKind }

// ResetSession forgets the transcript. The container keeps running.
type ResetSession struct{ fact }

func (ResetSession) Kind() Kind { return ResetSessionKind }
