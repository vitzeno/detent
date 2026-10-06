package ui

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// The undo question offers only what the checkpoint covers: no files
// question without a tree, and no container without a snapshot.
func TestUndo_AsksWhatTheCheckpointCovers(t *testing.T) {
	cases := []struct {
		name       string
		checkpoint event.CheckpointTaken
		hint, page string
		key        string
		revert     bool
	}{
		{"sandbox and files", event.CheckpointTaken{Snapshot: "s", Tree: "t"},
			"[n/enter] container only · [y] revert your files too", "container goes back either way", "y", true},
		{"files only, on the host", event.CheckpointTaken{Tree: "t"},
			"[n/enter] conversation only · [y] revert your files too", "conversation goes back either way", "enter", false},
		{"sandbox only", event.CheckpointTaken{Snapshot: "s"},
			"[y/enter] undo · [n/esc] cancel", "were not checkpointed", "y", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := newKeyed(t)
			turn := uuid.Must(uuid.NewV7())
			c.checkpoint.Turn = turn
			for _, ev := range []event.Event{
				event.TurnStarted{Turn: turn, N: 1, Prompt: "p"}, c.checkpoint,
				event.TurnEnded{Turn: turn, Reason: event.EndDone},
			} {
				k.m.apply(ev)
			}
			k.m, _ = k.m.runUndo("/undo")
			require.Equal(t, modeUndo, k.m.mode)
			assert.Contains(t, k.m.statusHint(), c.hint)
			assert.Contains(t, stripANSI(strings.Join(k.m.undoLines(), "\n")), c.page)

			k.press(t, c.key)
			got, ok := k.intent(t).(event.RequestRollback)
			require.True(t, ok)
			assert.Equal(t, c.revert, got.RevertFiles)
		})
	}
}

// A checkpoint restores a container but cannot un-file an issue, and
// doing less than a human expects is the worst thing here.
func TestUndoPage_NamesWhatItCannotReverse(t *testing.T) {
	got, _ := undoTurn(t).runUndo("/undo 1")
	page := stripANSI(strings.Join(got.undoLines(), "\n"))

	assert.Contains(t, page, "2 tool call(s) will be undone")
	assert.Contains(t, page, "1 tool call(s) cannot be undone")
	assert.Contains(t, page, "github__create_issue", "the standing call is not named")
	assert.Contains(t, page, "go test ./...")
}

// A subagent's server call is the request's too, so undo says it stands.
func TestUndoPage_NamesWhatASubagentCannotReverse(t *testing.T) {
	turn, spawn, agent, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m := feed(t, event.SessionStarted{Model: "m"},
		event.TurnStarted{Turn: turn, N: 1, Prompt: "file the bug"},
		event.ToolCallProposed{ToolCall: spawn, Tool: event.ToolSpawnAgent, Args: map[string]any{"task": "file it"}},
		event.AgentStarted{Agent: agent, ToolCall: spawn, Name: "filer"},
		event.ToolCallProposed{ToolCall: call, Tool: "github__create_issue", Executor: "github", Agent: agent},
		event.ToolCallEnded{ToolCall: call},
		event.AgentEnded{Agent: agent, Reason: event.AgentDone},
		event.ToolCallEnded{ToolCall: spawn, Result: event.Result{Stdout: "filed"}},
		event.CheckpointTaken{Turn: turn, Tree: "t"},
		event.TurnEnded{Turn: turn, Reason: event.EndDone})
	m.layout.width, m.layout.height = 150, 45
	m.sizeViewport()

	shown, _ := m.runUndo("/undo 1")
	page := stripANSI(strings.Join(shown.undoLines(), "\n"))
	assert.Contains(t, page, "1 tool call(s) cannot be undone")
	assert.Contains(t, page, "github__create_issue")
}

// Nothing to warn about, nothing said: the section only earns its
// space when a Turn actually holds one.
func TestUndoPage_SaysNothingWhenEverythingReverses(t *testing.T) {
	turn := uuid.Must(uuid.NewV7())
	m := feed(t, event.SessionStarted{Model: "m"},
		event.TurnStarted{Turn: turn, N: 1, Prompt: "just a command"})
	call := uuid.Must(uuid.NewV7())
	m.apply(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "ls"}})
	m.apply(event.ToolCallEnded{ToolCall: call, Result: event.Result{}})
	m.apply(event.CheckpointTaken{Turn: turn})
	m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone})
	m.layout.width, m.layout.height = 150, 45
	m.sizeViewport()

	shown, _ := m.runUndo("/undo 1")
	page := stripANSI(strings.Join(shown.undoLines(), "\n"))
	assert.NotContains(t, page, "cannot be undone")
	assert.Contains(t, page, "1 tool call(s) will be undone")
}

// undoTurn is a request holding both kinds of tool call.
func undoTurn(t *testing.T) Model {
	t.Helper()
	turn := uuid.Must(uuid.NewV7())
	m := feed(t, event.SessionStarted{Model: "m"},
		event.TurnStarted{Turn: turn, N: 1, Prompt: "do several things"})
	for _, c := range []struct {
		tool, exec string
		args       map[string]any
	}{
		{"bash", "", map[string]any{"command": "go test ./..."}},
		{"github__create_issue", "github", map[string]any{"repo": "detent"}},
		{"write_file", "", map[string]any{"path": "notes.md"}},
	} {
		call := uuid.Must(uuid.NewV7())
		m.apply(event.ToolCallProposed{ToolCall: call, Tool: c.tool, Args: c.args, Executor: c.exec})
		m.apply(event.ToolCallEnded{ToolCall: call, Result: event.Result{Stdout: "ok"}})
	}
	m.apply(event.CheckpointTaken{Turn: turn})
	m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone})
	m.layout.width, m.layout.height = 150, 45
	m.sizeViewport()
	return m
}
