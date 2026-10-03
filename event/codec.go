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

// Decode rebuilds an event from its Kind and payload.
func Decode(k Kind, payload []byte) (Event, error) {
	decode, ok := codecs[k]
	if !ok {
		return nil, fmt.Errorf("event: no type registered for kind %q", k)
	}
	return decode(payload)
}

// Kinds is every kind that can be decoded, which is every kind, sorted.
func Kinds() []Kind { return slices.Sorted(maps.Keys(codecs)) }

// Subject is which Turn and Call a fact is about, read off the fields
// rather than switched on, since a switch is a second list.
func Subject(e Event) (turn, call uuid.UUID) {
	v := reflect.ValueOf(e)
	if v.Kind() != reflect.Struct {
		return turn, call
	}
	read := func(name string) (out uuid.UUID) {
		f := v.FieldByName(name)
		if f.IsValid() && f.Type() == reflect.TypeFor[uuid.UUID]() {
			out = f.Interface().(uuid.UUID)
		}
		return out
	}
	return read("Turn"), read("Call")
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
	SessionStartedKind:  codec[SessionStarted],
	SessionResumedKind:  codec[SessionResumed],
	SessionResetKind:    codec[SessionReset],
	TurnStartedKind:     codec[TurnStarted],
	TurnEndedKind:       codec[TurnEnded],
	CheckpointTakenKind: codec[CheckpointTaken],
	RolledBackKind:      codec[RolledBack],
	BoundReachedKind:    codec[BoundReached],
	StepStartedKind:     codec[StepStarted],
	StepEndedKind:       codec[StepEnded],
	ModelTextKind:       codec[ModelText],
	AppendedKind:        codec[Appended],
	CompactedKind:       codec[Compacted],
	MeasuredKind:        codec[ContextMeasured],
	CallProposedKind:    codec[CallProposed],
	CallAssessedKind:    codec[CallAssessed],
	ApprovalAskedKind:   codec[ApprovalAsked],
	CallStartedKind:     codec[CallStarted],
	OutputChunkKind:     codec[OutputChunk],
	CallEndedKind:       codec[CallEnded],
	CallJudgedKind:      codec[CallJudged],
	ViewReadyKind:       codec[ViewReady],
	ShellStartedKind:    codec[ShellStarted],
	ShellEndedKind:      codec[ShellEnded],
	NoticeKind:          codec[Notice],
	SessionsListedKind:  codec[SessionsListed],

	AuthorizationWaitingKind: codec[AuthorizationWaiting],
	ServerAuthorizedKind:     codec[ServerAuthorized],
	AuthorizationFailedKind:  codec[AuthorizationFailed],

	SubmitPromptKind:    codec[SubmitPrompt],
	ResolveApprovalKind: codec[ResolveApproval],
	NoteContextKind:     codec[NoteContext],
	AbortKind:           codec[Abort],
	RequestStopKind:     codec[RequestStop],
	ContinueKind:        codec[Continue],
	RequestRollbackKind: codec[RequestRollback],
	ResetSessionKind:    codec[ResetSession],
	ListSessionsKind:    codec[ListSessions],
	ListServersKind:     codec[ListServers],
	MeasureContextKind:  codec[MeasureContext],
	DeleteSessionKind:   codec[DeleteSession],
	ServersListedKind:   codec[ServersListed],
	RenameSessionKind:   codec[RenameSession],
	RunCommandKind:      codec[RunCommand],
	CancelCommandKind:   codec[CancelCommand],
	AuthorizeServerKind: codec[AuthorizeServer],
	OpenAuthKind:        codec[OpenAuthorization],
}
