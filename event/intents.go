package event

import "github.com/google/uuid"

// What anyone publishes, imperative. Each kind has exactly one owner
// subscribed to it.

// SubmitPrompt opens a Turn. Typed mid-Turn it becomes a NoteContext.
type SubmitPrompt struct {
	fact
	Text string `json:"Text"`
}

func (SubmitPrompt) Kind() Kind { return SubmitPromptKind }

// ResolveApproval answers one ApprovalAsked. The engine does not care
// whether a human, an auto-approver or a policy sent it.
type ResolveApproval struct {
	fact
	ToolCall uuid.UUID `json:"ToolCall"`
	Approved bool      `json:"Approved"`
}

func (ResolveApproval) Kind() Kind { return ResolveApprovalKind }

// NoteContext puts a message in the transcript with no tool run: how a
// human steers mid-Turn. Flushed at a Step boundary, never inside one.
type NoteContext struct {
	fact
	Text string `json:"Text"`
}

func (NoteContext) Kind() Kind { return NoteContextKind }

// RunCommand runs what the human typed. Not a request: nothing is
// assessed, nothing is approved, and no Turn opens.
type RunCommand struct {
	fact
	Text string `json:"Text"`
}

func (RunCommand) Kind() Kind { return RunCommandKind }

// CancelCommand stops one running command. Its own intent rather than
// a field on Abort, so no two subscribers read the same kind.
type CancelCommand struct {
	fact
	UserCommand uuid.UUID `json:"UserCommand"`
}

func (CancelCommand) Kind() Kind { return CancelCommandKind }

// Abort cancels in-flight tool calls and ends the Turn. The Step still
// completes, with a result per unrun tool call, or the next Step fails.
type Abort struct {
	fact
	Turn uuid.UUID `json:"Turn"`
}

func (Abort) Kind() Kind { return AbortKind }

// RequestStop is advisory, honoured at the next Step boundary. How the
// post-execution judge acts without becoming an interceptor.
type RequestStop struct {
	fact
	Turn   uuid.UUID `json:"Turn"`
	Reason string    `json:"Reason"`
}

func (RequestStop) Kind() Kind { return RequestStopKind }

// Continue answers BoundReached. False ends the Turn.
type Continue struct {
	fact
	Turn     uuid.UUID `json:"Turn"`
	Approved bool      `json:"Approved"`
}

func (Continue) Kind() Kind { return ContinueKind }

// RequestRollback restores the Turn's checkpoint and truncates the
// transcript to where it began. RevertFiles also reverts the workspace.
type RequestRollback struct {
	fact
	Turn        uuid.UUID `json:"Turn"`
	RevertFiles bool      `json:"RevertFiles"`
}

func (RequestRollback) Kind() Kind { return RequestRollbackKind }

// DeleteSession forgets a stored session: its events, its log and
// its container. Irreversible, so nothing sends it unasked.
type DeleteSession struct {
	fact
	Session uuid.UUID `json:"Session"`
}

func (DeleteSession) Kind() Kind { return DeleteSessionKind }

// ListServers asks which MCP servers are wired and how they fared.
// Whatever holds them answers, since a front-end cannot ask directly.
type ListServers struct{ fact }

func (ListServers) Kind() Kind { return ListServersKind }

// MeasureContext asks the engine for a ContextMeasured now, rather than
// after the next Step.
type MeasureContext struct{ fact }

func (MeasureContext) Kind() Kind { return MeasureContextKind }

// AuthorizeServer asks for a fresh sign-in. Human-triggered, always:
// nothing else may make a server register a client.
type AuthorizeServer struct {
	fact
	Server string `json:"Server"`
}

func (AuthorizeServer) Kind() Kind { return AuthorizeServerKind }

// OpenAuthorization asks for a waiting sign-in's link to be opened in
// a browser. Whatever holds the link opens it, since ui runs no processes.
type OpenAuthorization struct {
	fact
	Server string `json:"Server"`
}

func (OpenAuthorization) Kind() Kind { return OpenAuthKind }

// ListSessions asks what can be resumed. Whatever holds the log
// answers with SessionsListed.
type ListSessions struct{ fact }

func (ListSessions) Kind() Kind { return ListSessionsKind }

// RenameSession gives a session a name a human will recognise. Not in the
// log: the name is the header's own, and its holder answers with a listing.
type RenameSession struct {
	fact
	Session uuid.UUID `json:"Session"`
	Name    string    `json:"Name"`
}

func (RenameSession) Kind() Kind { return RenameSessionKind }

// ResetSession forgets the transcript. The container keeps running.
type ResetSession struct{ fact }

func (ResetSession) Kind() Kind { return ResetSessionKind }
