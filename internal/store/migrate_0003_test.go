package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// A database written before Call became ToolCall and Shell became
// UserCommand opens with every record in today's shape, after a backup.
func TestMigrate_0003RewritesSessionsSavedUnderTheOldNames(t *testing.T) {
	file := filepath.Join(t.TempDir(), "events.db")
	session := uuid.MustParse("01a101e1-fc16-7dbb-a21f-bf8ee5da28d5")
	id := uuid.MustParse("01a101e1-fc16-7dcb-a97a-a4d28284b10d").String()

	old := []struct{ kind, payload string }{
		{"call.proposed", `{"Call":"` + id + `","Tool":"bash"}`},
		{"call.assessed", `{"Call":"` + id + `"}`},
		{"call.approval", `{"Call":"` + id + `"}`},
		{"call.started", `{"Call":"` + id + `","Runner":"host"}`},
		{"call.ended", `{"Call":"` + id + `","Result":{"ExitCode":2}}`},
		{"call.judged", `{"Call":"` + id + `","Status":"ok"}`},
		{"call.view", `{"Call":"` + id + `","Source":"saved"}`},
		{"shell.started", `{"Shell":"` + id + `","Command":"ls"}`},
		{"shell.ended", `{"Shell":"` + id + `"}`},
		{"step.ended", `{"Calls":3,"Stop":"stop"}`},
		{"turn.bound", `{"Steps":100,"Calls":140}`},
		{"transcript.appended", `{"Messages":[{"Role":"assistant","Calls":[{"ID":"c1","Name":"bash","Args":{"Call":"kept"}}]},` +
			`{"Role":"tool","CallID":"c1","Content":"\"Calls\": left alone"}]}`},
		{"notice", `{"Level":"info","Text":"untouched"}`},
	}
	writeAtVersion2(t, file, session, old)

	s, err := Open(file)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	_, err = os.Stat(file + ".bak-v2")
	require.NoError(t, err, "the database was copied before it was rewritten")
	assert.Equal(t, s.schema, userVersion(t, s.db))
	_, err = s.db.Exec(`SELECT tool_call FROM events LIMIT 1`)
	require.NoError(t, err, "the lifted column has the new name")
	var schema int
	require.NoError(t, s.db.QueryRow(`SELECT schema FROM sessions WHERE id = ?`, session.String()).Scan(&schema))
	assert.Equal(t, 3, schema, "the session says its records are in the shape 0003 left them")

	got, err := s.Replay(session)
	require.NoError(t, err)
	want := []event.Event{
		event.ToolCallProposed{ToolCall: uuid.MustParse(id), Tool: "bash"},
		event.ToolCallAssessed{ToolCall: uuid.MustParse(id)},
		event.ApprovalAsked{ToolCall: uuid.MustParse(id)},
		event.ToolCallStarted{ToolCall: uuid.MustParse(id), Runner: "host"},
		event.ToolCallEnded{ToolCall: uuid.MustParse(id), Result: event.Result{ExitCode: 2}},
		event.ToolCallJudged{ToolCall: uuid.MustParse(id), Status: "ok"},
		event.ViewReady{ToolCall: uuid.MustParse(id), Source: "saved"},
		event.UserCommandStarted{UserCommand: uuid.MustParse(id), Command: "ls"},
		event.UserCommandEnded{UserCommand: uuid.MustParse(id)},
		event.StepEnded{ToolCalls: 3, Stop: "stop"},
		event.BoundReached{Steps: 100, ToolCalls: 140},
		event.Appended{Messages: []event.Message{
			{Role: event.RoleAssistant, Requests: []event.ToolRequest{{ID: "c1", Name: "bash", Args: map[string]any{"Call": "kept"}}}},
			{Role: event.RoleTool, RequestID: "c1", Content: `"Calls": left alone`},
		}},
		event.Notice{Level: "info", Text: "untouched"},
	}
	require.Len(t, got, len(want))
	for i, r := range got {
		assert.Equal(t, want[i], r.Event, "record %d, once %s", i, old[i].kind)
	}
}

// writeAtVersion2 lays down a database as a build before 0003 left it.
func writeAtVersion2(t *testing.T, file string, session uuid.UUID, records []struct{ kind, payload string }) {
	t.Helper()
	db, err := sql.Open("sqlite", file+pragmas)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	_, err = shipped(t, db).UpTo(t.Context(), 2)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO sessions (id, started, model, sandbox, network) VALUES (?, 0, 'm', 0, 0)`, session.String())
	require.NoError(t, err)
	for i, r := range records {
		_, err = db.Exec(`INSERT INTO events (session, ordinal, at, kind, payload) VALUES (?, ?, 0, ?, ?)`,
			session.String(), i+1, r.kind, []byte(r.payload))
		require.NoError(t, err)
	}
}
