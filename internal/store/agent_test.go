package store

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// childWork is every record of one child: its own, and its tool calls' later
// ones, which carry no Agent and are found through their proposal.
const childWork = `SELECT e.ordinal FROM events e
  LEFT JOIN events p ON p.session = e.session AND p.tool_call = e.tool_call AND p.kind = ?
  WHERE e.session = ? AND (e.agent = ? OR p.agent = ?) ORDER BY e.ordinal`

// Everything recorded before agents existed was the root's.
func TestMigrate0004_LeavesOldRowsAsRoot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "events.db")
	session := uuid.Must(uuid.NewV7())
	writeAtVersion2(t, file, session, []struct{ kind, payload string }{
		{"notice", `{"Level":"info","Text":"old"}`},
	})
	s, err := Open(file)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var agent *string
	require.NoError(t, s.db.QueryRow(`SELECT agent FROM events WHERE session = ?`, session.String()).Scan(&agent))
	assert.Nil(t, agent)
}

// A child's records come back by its id, and the spawn call that started it
// stays the parent's.
func TestAppend_FillsAgentFromTheFact(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "events.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	session, agent := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	spawn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for i, e := range []event.Event{
		event.SessionStarted{Session: session, Model: "m"},
		event.ToolCallProposed{ToolCall: spawn, Tool: "spawn_agent"},
		event.AgentStarted{Agent: agent, ToolCall: spawn, Name: "look"},
		event.ToolCallProposed{ToolCall: call, Tool: "grep", Agent: agent},
		event.ToolCallEnded{ToolCall: call},
		event.AgentEnded{Agent: agent, Reason: event.AgentDone},
		event.ToolCallEnded{ToolCall: spawn},
	} {
		require.NoError(t, s.Append(session, event.Record{Ordinal: uint64(i + 1), Event: e}))
	}

	rows, err := s.db.Query(childWork, string(event.ToolCallProposedKind), session.String(), agent.String(), agent.String())
	require.NoError(t, err)
	defer rows.Close() //nolint:errcheck // a test's rows
	var got []int
	for rows.Next() {
		var n int
		require.NoError(t, rows.Scan(&n))
		got = append(got, n)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []int{3, 4, 5, 6}, got, "the child's whole work, and none of the parent's")
}
