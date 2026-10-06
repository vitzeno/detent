package event

import "github.com/google/uuid"

// What anyone publishes, imperative. Each kind has exactly one owner
// subscribed to it.

// The engine's, which drive the agent loop.

// SubmitPrompt opens a Turn. Typed mid-Turn it becomes a NoteContext.
type SubmitPrompt struct {
	fact
	Text string `json:"Text"`
}

func (SubmitPrompt) Kind() Kind { return SubmitPromptKind }

// NoteContext puts a message in the transcript with no tool run: how a
// human steers mid-Turn. Flushed at a Step boundary, never inside one.
type NoteContext struct {
	fact
	Text string `json:"Text"`
}

func (NoteContext) Kind() Kind { return NoteContextKind }

// ResolveApproval answers one ApprovalAsked. The engine does not care
// whether a human, an auto-approver or a policy sent it.
type ResolveApproval struct {
	fact
	ToolCall uuid.UUID `json:"ToolCall"`
	Approved bool      `json:"Approved"`
}

func (ResolveApproval) Kind() Kind { return ResolveApprovalKind }

// Abort cancels in-flight tool calls and ends the Turn. The Step still
// completes, with a result per unrun tool call, or the next Step fails.
type Abort struct {
	fact
	Turn uuid.UUID `json:"Turn"`
}

func (Abort) Kind() Kind { return AbortKind }

// SuggestFinish tells the model, at the next Step, that the judge reads its
// request as answered. Advice, not a stop: the model decides.
type SuggestFinish struct {
	fact
	Turn   uuid.UUID `json:"Turn"`
	Reason string    `json:"Reason"`
}

func (SuggestFinish) Kind() Kind { return SuggestFinishKind }

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

// ResetSession forgets the transcript. The container keeps running.
type ResetSession struct{ fact }

func (ResetSession) Kind() Kind { return ResetSessionKind }

// ResumeSession leaves this session as stored and continues another, once
// no request is running. The container keeps running, as for a reset.
type ResumeSession struct {
	fact
	Session uuid.UUID `json:"Session"`
}

func (ResumeSession) Kind() Kind { return ResumeSessionKind }

// ReviewChanges asks a reviewer agent to comment on Files, the diff the human
// sees, once no request is running. Asked is what the changes were made for.
type ReviewChanges struct {
	fact
	Review   uuid.UUID   `json:"Review"`
	Reviewed uuid.UUID   `json:"Reviewed"`
	Scope    ReviewScope `json:"Scope"`
	Base     string      `json:"Base"`
	Head     string      `json:"Head"`
	Against  string      `json:"Against"`
	Request  int         `json:"Request"`
	Asked    string      `json:"Asked"`
	Files    []FileDiff  `json:"-"`
}

func (ReviewChanges) Kind() Kind { return ReviewChangesKind }

// MeasureContext asks the engine for a ContextMeasured now, rather than
// after the next Step.
type MeasureContext struct{ fact }

func (MeasureContext) Kind() Kind { return MeasureContextKind }

// The store's, which holds every session's log.

// ListSessions asks what can be resumed. Whatever holds the log
// answers with SessionsListed.
type ListSessions struct{ fact }

func (ListSessions) Kind() Kind { return ListSessionsKind }

// LoadSession asks for a stored session's records, to look at before
// resuming. Whatever holds the log answers with SessionLoaded.
type LoadSession struct {
	fact
	Session uuid.UUID `json:"Session"`
}

func (LoadSession) Kind() Kind { return LoadSessionKind }

// LoadDiff asks for the changes from Base to Head, an empty Head meaning the
// files now, or with Branch the branch's against Against, main when empty.
// internal/review answers with DiffLoaded.
type LoadDiff struct {
	fact
	Base    string `json:"Base"`
	Head    string `json:"Head"`
	Branch  bool   `json:"Branch"`
	Against string `json:"Against"`
}

func (LoadDiff) Kind() Kind { return LoadDiffKind }

// CommentReview adds, edits or deletes one comment. internal/review answers with
// ReviewCommented, which is what is stored and what the modal draws.
type CommentReview struct {
	fact
	Review   uuid.UUID     `json:"Review"`
	Reviewed uuid.UUID     `json:"Reviewed"`
	Base     string        `json:"Base"`
	Head     string        `json:"Head"`
	Scope    ReviewScope   `json:"Scope"`
	Against  string        `json:"Against"`
	Op       CommentOp     `json:"Op"`
	Comment  ReviewComment `json:"Comment"`
}

func (CommentReview) Kind() Kind { return CommentReviewKind }

// SubmitReview sends a review's comments to the agent as its next prompt, the
// human's as instructions and a reviewer's as opinions. Request is the number
// of the request reviewed, or the last one for a wider scope.
type SubmitReview struct {
	fact
	Review   uuid.UUID       `json:"Review"`
	Scope    ReviewScope     `json:"Scope"`
	Request  int             `json:"Request"`
	Against  string          `json:"Against"`
	Comments []ReviewComment `json:"Comments"`
}

func (SubmitReview) Kind() Kind { return SubmitReviewKind }

// RenameSession gives a session a name a human will recognise. Not in the
// log: the name is the header's own, and its holder answers with a listing.
type RenameSession struct {
	fact
	Session uuid.UUID `json:"Session"`
	Name    string    `json:"Name"`
}

func (RenameSession) Kind() Kind { return RenameSessionKind }

// forget's, which destroys what a session left.

// DeleteSession forgets a stored session: its events, its log and
// its container. Irreversible, so nothing sends it unasked.
type DeleteSession struct {
	fact
	Session uuid.UUID `json:"Session"`
}

func (DeleteSession) Kind() Kind { return DeleteSessionKind }

// usercommand's, for commands the human runs.

// RunCommand runs what the human typed. Not a request: nothing is
// assessed, nothing is approved, and no Turn opens.
type RunCommand struct {
	fact
	Text string `json:"Text"`
}

func (RunCommand) Kind() Kind { return RunCommandKind }

// StopAgent stops one child, which still reports what it found. Its own
// intent rather than a field on Abort, which stops everything.
type StopAgent struct {
	fact
	Agent uuid.UUID `json:"Agent"`
}

func (StopAgent) Kind() Kind { return StopAgentKind }

// CancelCommand stops one running command. Its own intent rather than
// a field on Abort, so no two subscribers read the same kind.
type CancelCommand struct {
	fact
	UserCommand uuid.UUID `json:"UserCommand"`
}

func (CancelCommand) Kind() Kind { return CancelCommandKind }

// mcp's, for the MCP servers and their sign-ins.

// ListServers asks which MCP servers are wired and how they fared.
// Whatever holds them answers, since a front-end cannot ask directly.
type ListServers struct{ fact }

func (ListServers) Kind() Kind { return ListServersKind }

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
