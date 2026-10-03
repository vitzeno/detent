package event

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// TODO(remove): delete this file once a store migration has rewritten
// every record saved under the old names, so nothing on disk still needs it.
//
// What it does: the "Call" to "ToolCall" and "Shell" to "UserCommand" rename
// changed event kinds and JSON keys, and events.db stores both verbatim
// (no struct tags). Decode passes every stored record through upgrade,
// which renames an old kind and the keys that kind carried, before the
// codec sees it. Three shapes need it:
//   - the ten renamed kinds ("call.ended" and so on): top-level "Call"
//     becomes "ToolCall" and "Shell" becomes "UserCommand"
//   - step.ended and turn.bound kept their kind, but "Calls" became "ToolCalls"
//   - transcript.appended kept its kind, but each message's "Calls" became
//     "Requests" and "CallID" became "RequestID"
// Only those keys at those levels move, so a tool argument or output that
// happens to contain "Call" is never touched. New records are written in
// the new shape, and a current record passes through unchanged.

// legacyKinds maps a kind as it was once stored to what it is now.
var legacyKinds = map[Kind]Kind{
	"call.proposed": ToolCallProposedKind,
	"call.assessed": ToolCallAssessedKind,
	"call.approval": ApprovalAskedKind,
	"call.started":  ToolCallStartedKind,
	"call.output":   OutputChunkKind,
	"call.ended":    ToolCallEndedKind,
	"call.judged":   ToolCallJudgedKind,
	"call.view":     ViewReadyKind,
	"shell.started": UserCommandStartedKind,
	"shell.ended":   UserCommandEndedKind,
}

// Only these keys move, and only where the struct put them, so a tool's
// own argument or output that happens to say "Call" is left alone.
var (
	legacyFields   = map[string]string{"Call": "ToolCall", "Shell": "UserCommand"}
	legacyMessages = map[string]string{"Calls": "Requests", "CallID": "RequestID"}
	// Kinds that kept their name but not a count's.
	legacyCounts = map[Kind]map[string]string{
		StepEndedKind:    {"Calls": "ToolCalls"},
		BoundReachedKind: {"Calls": "ToolCalls"},
	}
)

// upgrade turns an old record into the current shape, and passes a
// current one through untouched.
func upgrade(k Kind, payload []byte) (Kind, []byte, error) {
	if now, ok := legacyKinds[k]; ok {
		p, err := renameKeys(payload, legacyFields)
		return now, p, err
	}
	if names, ok := legacyCounts[k]; ok && bytes.Contains(payload, []byte(`"Calls"`)) {
		p, err := renameKeys(payload, names)
		return k, p, err
	}
	if k == AppendedKind && (bytes.Contains(payload, []byte(`"Calls"`)) || bytes.Contains(payload, []byte(`"CallID"`))) {
		p, err := upgradeMessages(payload)
		return k, p, err
	}
	return k, payload, nil
}

func renameKeys(payload []byte, names map[string]string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, fmt.Errorf("event: upgrade: %w", err)
	}
	for old, now := range names {
		if v, ok := fields[old]; ok {
			delete(fields, old)
			fields[now] = v
		}
	}
	return json.Marshal(fields)
}

func upgradeMessages(payload []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, fmt.Errorf("event: upgrade: %w", err)
	}
	raw, ok := fields["Messages"]
	if !ok {
		return payload, nil
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, fmt.Errorf("event: upgrade messages: %w", err)
	}
	for i, m := range messages {
		up, err := renameKeys(m, legacyMessages)
		if err != nil {
			return nil, err
		}
		messages[i] = up
	}
	raw, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	fields["Messages"] = raw
	return json.Marshal(fields)
}
