package event

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/google/uuid"
)

// Encoding lives here, not in a store: one holding its own type list
// would decode every old record and silently drop a new one.

// Encode is an event's own fields as JSON. The Kind travels beside it,
// not inside, because a reader needs it to choose the type.
func Encode(e Event) ([]byte, error) { return json.Marshal(e) }

// Decode rebuilds an event from its Kind and payload. A record in an older
// shape is the store's to migrate, never this package's to guess at.
func Decode(k Kind, payload []byte) (Event, error) {
	decode, ok := codecs[k]
	if !ok {
		return nil, fmt.Errorf("event: no type registered for kind %q", k)
	}
	return decode(payload)
}

// Kinds is every kind that can be decoded, which is every kind, sorted.
func Kinds() []Kind { return slices.Sorted(maps.Keys(codecs)) }

// Subject is which Turn, ToolCall and Agent a fact is about, read off the
// fields rather than switched on, since a switch is a second list.
func Subject(e Event) (turn, call, agent uuid.UUID) {
	v := reflect.ValueOf(e)
	if v.Kind() != reflect.Struct {
		return turn, call, agent
	}
	read := func(name string) (out uuid.UUID) {
		f := v.FieldByName(name)
		if f.IsValid() {
			out, _ = f.Interface().(uuid.UUID)
		}
		return out
	}
	return read("Turn"), read("ToolCall"), read("Agent")
}

func codec[T Event](payload []byte) (Event, error) {
	var v T
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, fmt.Errorf("event: decode %T: %w", v, err)
	}
	return v, nil
}

// codecs is the one place a new event type must be added.
var codecs = map[Kind]func([]byte) (Event, error){
	SessionStartedKind: codec[SessionStarted],
	SessionResumedKind: codec[SessionResumed],
	SessionResetKind:   codec[SessionReset],
	SessionsListedKind: codec[SessionsListed],
	SessionLoadedKind:  codec[SessionLoaded],
	NoticeKind:         codec[Notice],

	TurnStartedKind:     codec[TurnStarted],
	CheckpointTakenKind: codec[CheckpointTaken],
	TurnEndedKind:       codec[TurnEnded],
	BoundReachedKind:    codec[BoundReached],
	RolledBackKind:      codec[RolledBack],

	AgentStartedKind: codec[AgentStarted],
	AgentEndedKind:   codec[AgentEnded],

	StepStartedKind: codec[StepStarted],
	StepEndedKind:   codec[StepEnded],
	ModelTextKind:   codec[ModelText],

	AppendedKind:  codec[Appended],
	CompactedKind: codec[Compacted],
	MeasuredKind:  codec[ContextMeasured],

	ToolCallProposedKind: codec[ToolCallProposed],
	ToolCallAssessedKind: codec[ToolCallAssessed],
	ApprovalAskedKind:    codec[ApprovalAsked],
	ToolCallStartedKind:  codec[ToolCallStarted],
	OutputChunkKind:      codec[OutputChunk],
	ToolCallEndedKind:    codec[ToolCallEnded],
	ToolCallJudgedKind:   codec[ToolCallJudged],
	ViewReadyKind:        codec[ViewReady],

	UserCommandStartedKind: codec[UserCommandStarted],
	UserCommandEndedKind:   codec[UserCommandEnded],

	DiffLoadedKind:      codec[DiffLoaded],
	ReviewStartedKind:   codec[ReviewStarted],
	ReviewCommentedKind: codec[ReviewCommented],
	ReviewSubmittedKind: codec[ReviewSubmitted],

	ServersListedKind:        codec[ServersListed],
	AuthorizationWaitingKind: codec[AuthorizationWaiting],
	ServerAuthorizedKind:     codec[ServerAuthorized],
	AuthorizationFailedKind:  codec[AuthorizationFailed],

	SubmitPromptKind:    codec[SubmitPrompt],
	NoteContextKind:     codec[NoteContext],
	ResolveApprovalKind: codec[ResolveApproval],
	AbortKind:           codec[Abort],
	SuggestFinishKind:   codec[SuggestFinish],
	ContinueKind:        codec[Continue],
	RequestRollbackKind: codec[RequestRollback],
	ResetSessionKind:    codec[ResetSession],
	MeasureContextKind:  codec[MeasureContext],
	ResumeSessionKind:   codec[ResumeSession],
	ReviewChangesKind:   codec[ReviewChanges],

	ListSessionsKind:  codec[ListSessions],
	RenameSessionKind: codec[RenameSession],
	LoadSessionKind:   codec[LoadSession],
	LoadDiffKind:      codec[LoadDiff],
	CommentReviewKind: codec[CommentReview],
	SubmitReviewKind:  codec[SubmitReview],
	DeleteSessionKind: codec[DeleteSession],

	RunCommandKind:    codec[RunCommand],
	CancelCommandKind: codec[CancelCommand],
	StopAgentKind:     codec[StopAgent],

	ListServersKind:     codec[ListServers],
	AuthorizeServerKind: codec[AuthorizeServer],
	OpenAuthKind:        codec[OpenAuthorization],
}
