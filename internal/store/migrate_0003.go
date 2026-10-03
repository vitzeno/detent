package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// toolCallNames rewrites records saved before Call became ToolCall and Shell
// became UserCommand, renames the lifted column and marks every session as at 3.
func toolCallNames(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `ALTER TABLE events RENAME COLUMN call TO tool_call`); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT session, ordinal, kind, payload FROM events
	  WHERE kind IN ('call.proposed', 'call.assessed', 'call.approval', 'call.started', 'call.output',
	    'call.ended', 'call.judged', 'call.view', 'shell.started', 'shell.ended',
	    'step.ended', 'turn.bound', 'transcript.appended')`)
	if err != nil {
		return err
	}
	type row struct {
		session string
		ordinal int64
		kind    string
		payload []byte
	}
	var old []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.session, &r.ordinal, &r.kind, &r.payload); err != nil {
			_ = rows.Close() // the scan error is the one worth reporting
			return err
		}
		old = append(old, r)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range old {
		kind, payload, err := upgrade0003(r.kind, r.payload)
		if err != nil {
			return fmt.Errorf("session %s record %d: %w", r.session, r.ordinal, err)
		}
		if kind == r.kind && bytes.Equal(payload, r.payload) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE events SET kind = ?, payload = ? WHERE session = ? AND ordinal = ?`,
			kind, payload, r.session, r.ordinal); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE sessions SET schema = 3`)
	return err
}

// The rename as it stood. Only these keys at these levels move, so a tool's
// own argument or output that happens to say "Call" is never touched.
var (
	kinds0003 = map[string]string{
		"call.proposed": "tool_call.proposed", "call.assessed": "tool_call.assessed",
		"call.approval": "tool_call.approval", "call.started": "tool_call.started",
		"call.output": "output.chunk", "call.ended": "tool_call.ended",
		"call.judged": "tool_call.judged", "call.view": "view.ready",
		"shell.started": "user_command.started", "shell.ended": "user_command.ended",
	}
	ids0003      = map[string]string{"Call": "ToolCall", "Shell": "UserCommand"}
	counts0003   = map[string]string{"Calls": "ToolCalls"}
	messages0003 = map[string]string{"Calls": "Requests", "CallID": "RequestID"}
)

func upgrade0003(kind string, payload []byte) (string, []byte, error) {
	switch {
	case kinds0003[kind] != "":
		p, err := renameKeys(payload, ids0003)
		return kinds0003[kind], p, err
	case kind == "step.ended" || kind == "turn.bound":
		p, err := renameKeys(payload, counts0003)
		return kind, p, err
	case kind == "transcript.appended":
		p, err := renameMessageKeys(payload, messages0003)
		return kind, p, err
	}
	return kind, payload, nil
}

// renameKeys moves top-level keys, and leaves a payload that has none alone.
func renameKeys(payload []byte, names map[string]string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, err
	}
	moved := false
	for from, to := range names {
		if v, ok := fields[from]; ok {
			delete(fields, from)
			fields[to] = v
			moved = true
		}
	}
	if !moved {
		return payload, nil
	}
	return json.Marshal(fields)
}

func renameMessageKeys(payload []byte, names map[string]string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, err
	}
	raw, ok := fields["Messages"]
	if !ok {
		return payload, nil
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, err
	}
	moved := false
	for i, m := range messages {
		up, err := renameKeys(m, names)
		if err != nil {
			return nil, err
		}
		moved = moved || !bytes.Equal(up, m)
		messages[i] = up
	}
	if !moved {
		return payload, nil
	}
	out, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	fields["Messages"] = out
	return json.Marshal(fields)
}
