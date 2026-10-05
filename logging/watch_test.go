package logging_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/logging"
)

// The whole of how a session gets logged: facts in, records out, with
// correlation off the event rather than threaded through call sites.
func TestWatch_LogsEveryFactWithItsIDs(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("w1", logging.WithDir(dir), logging.WithLevel("debug"))
	require.NoError(t, err)

	bus := event.New()
	stop := logging.Watch(bus)

	turn, step, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	bus.Publish(event.TurnStarted{Turn: turn, N: 1, Prompt: "go"})
	bus.Publish(event.StepStarted{Turn: turn, Step: step, N: 1})
	bus.Publish(event.ToolCallProposed{ToolCall: call, Step: step, Tool: "bash"})
	bus.Publish(event.ToolCallEnded{ToolCall: call, Result: event.Result{ExitCode: 1, Stderr: "boom"},
		Took: 12 * time.Millisecond})
	bus.Publish(event.TurnEnded{Turn: turn, Reason: event.EndDone})

	bus.Settle(2 * time.Second)
	require.Len(t, records(t, dir, "w1"), 5)
	stop()
	require.NoError(t, closer())

	got := records(t, dir, "w1")
	by := byEvent(t, got)

	assert.Equal(t, turn.String(), by["turn.started"][logging.KeyTurn])
	assert.Equal(t, step.String(), by["step.started"][logging.KeyStep])
	assert.Equal(t, call.String(), by["tool_call.proposed"][logging.KeyToolCall],
		"a call record names its call, and nothing had to thread it")

	ended := by["tool_call.ended"]
	assert.EqualValues(t, 1, ended["exit"])
	assert.EqualValues(t, 12, ended[logging.KeyMS])
	assert.Equal(t, "WARN", ended["level"], "a failed call is worth finding")

	for _, r := range got {
		assert.Equal(t, "w1", r[logging.KeySession])
		assert.NotZero(t, r[logging.KeyOrdinal], "an ordinal places the record in the bus's order")
	}
}

// A child's records say whose they are, its tool calls' later ones included,
// and the root's say nothing.
func TestWatch_TagsAChildsRecordsWithItsAgent(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("w2", logging.WithDir(dir), logging.WithLevel("debug"))
	require.NoError(t, err)
	bus := event.New()
	stop := logging.Watch(bus)

	agent, spawn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	bus.Publish(event.ToolCallProposed{ToolCall: spawn, Tool: "spawn_agent"})
	bus.Publish(event.AgentStarted{Agent: agent, ToolCall: spawn, Name: "explore", Task: "look"})
	bus.Publish(event.ToolCallProposed{ToolCall: call, Tool: "grep", Agent: agent})
	bus.Publish(event.ToolCallEnded{ToolCall: call})
	bus.Publish(event.ToolCallEnded{ToolCall: spawn})
	bus.Settle(2 * time.Second)
	stop()
	require.NoError(t, closer())

	got := records(t, dir, "w2")
	require.Len(t, got, 5)
	want := []any{nil, agent.String(), agent.String(), agent.String(), nil}
	for i, r := range got {
		assert.Equal(t, want[i], r[logging.KeyAgent], "record %d, %s", i, r[logging.KeyEvent])
	}
}

// A Step says what caching saved, and a cost only when the endpoint sent one.
func TestWatch_LogsCacheAndOnlyAKnownCost(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("w3", logging.WithDir(dir), logging.WithLevel("debug"))
	require.NoError(t, err)
	bus := event.New()
	stop := logging.Watch(bus)
	bus.Publish(event.StepEnded{Step: uuid.Must(uuid.NewV7()), Usage: event.Usage{PromptTokens: 100, CachedTokens: 90,
		Cost: 0.01, HasCost: true}})
	bus.Publish(event.StepEnded{Step: uuid.Must(uuid.NewV7()), Usage: event.Usage{PromptTokens: 100}})
	bus.Settle(2 * time.Second)
	stop()
	require.NoError(t, closer())

	got := records(t, dir, "w3")
	require.Len(t, got, 2)
	assert.EqualValues(t, 90, got[0]["cached_tokens"])
	assert.InDelta(t, 0.01, got[0]["cost"], 1e-9)
	assert.NotContains(t, got[1], "cost", "unknown is not free")
}

// A session says which features it ran with, and an agent why it stopped,
// so a log answers both without the bodies log_bodies withholds.
func TestWatch_LogsWhatSubagentsNeed(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("w4", logging.WithDir(dir), logging.WithLevel("debug"))
	require.NoError(t, err)
	bus := event.New()
	stop := logging.Watch(bus)
	bus.Publish(event.SessionStarted{Model: "m", Subagents: true, MaxAgents: 10, Commit: "abc123"})
	bus.Publish(event.ToolCallProposed{ToolCall: uuid.Must(uuid.NewV7()), Tool: "github__list", Executor: "github"})
	bus.Publish(event.AgentEnded{Agent: uuid.Must(uuid.NewV7()), Reason: event.AgentPartial, Why: "ran out of context",
		Usage: event.Usage{Cost: 0.02, HasCost: true}})
	bus.Settle(2 * time.Second)
	stop()
	require.NoError(t, closer())

	by := byEvent(t, records(t, dir, "w4"))
	assert.Equal(t, true, by["session.started"]["subagents"])
	assert.Equal(t, "abc123", by["session.started"]["commit"])
	assert.Equal(t, "github", by["tool_call.proposed"]["executor"])
	assert.Equal(t, "ran out of context", by["agent.ended"]["why"])
	assert.InDelta(t, 0.02, by["agent.ended"]["cost"], 1e-9)
}

// A command the human ran is logged like a tool call, under its own key:
// the id has to be findable, and a failure has to be worth finding.
func TestWatch_LogsAHumanCommand(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("w5", logging.WithDir(dir),
		logging.WithLevel("debug"), logging.WithBodies(true))
	require.NoError(t, err)

	bus := event.New()
	stop := logging.Watch(bus)

	shell := uuid.Must(uuid.NewV7())
	bus.Publish(event.UserCommandStarted{UserCommand: shell, Command: "git status", Runner: "sandbox"})
	bus.Publish(event.UserCommandEnded{UserCommand: shell, Took: 8 * time.Millisecond,
		Result: event.Result{ExitCode: 1, Stderr: "boom"}})

	bus.Settle(2 * time.Second)
	require.Len(t, records(t, dir, "w5"), 2)
	stop()
	require.NoError(t, closer())

	by := byEvent(t, records(t, dir, "w5"))

	started := by["user_command.started"]
	assert.Equal(t, shell.String(), started[logging.KeyUserCommand])
	assert.Equal(t, "git status", started["command"])
	assert.Equal(t, "sandbox", started["runner"])

	ended := by["user_command.ended"]
	assert.Equal(t, shell.String(), ended[logging.KeyUserCommand])
	assert.EqualValues(t, 1, ended["exit"])
	assert.EqualValues(t, 8, ended[logging.KeyMS])
	assert.Equal(t, "WARN", ended["level"], "a failed command is worth finding")
}

// Live output is the one thing too noisy to keep, and ToolCallEnded
// carries the whole of it anyway.
func TestWatch_SkipsLiveOutput(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("w2", logging.WithDir(dir), logging.WithLevel("debug"))
	require.NoError(t, err)

	bus := event.New()
	stop := logging.Watch(bus)
	for range 50 {
		bus.Publish(event.OutputChunk{ToolCall: testID("c1"), Line: "noise"})
	}
	bus.Publish(event.Notice{Level: "info", Text: "done"})

	bus.Settle(2 * time.Second)
	require.Len(t, records(t, dir, "w2"), 1)
	stop()
	require.NoError(t, closer())

	for _, r := range records(t, dir, "w2") {
		assert.NotEqual(t, "output.chunk", r[logging.KeyEvent])
	}
}

// Prompts and output carry secrets and bulk, so they are withheld
// unless the session asked for them.
func TestWatch_WithholdsBodiesUnlessAsked(t *testing.T) {
	for _, bodies := range []bool{false, true} {
		dir := t.TempDir()
		closer, err := logging.Setup("b", logging.WithDir(dir), logging.WithLevel("debug"), logging.WithBodies(bodies))
		require.NoError(t, err)

		bus := event.New()
		stop := logging.Watch(bus)
		bus.Publish(event.TurnStarted{Turn: testID("t"), N: 1, Prompt: "the secret prompt"})

		bus.Settle(2 * time.Second)
		require.Len(t, records(t, dir, "b"), 1)
		stop()
		require.NoError(t, closer())

		got := records(t, dir, "b")[0]["prompt"]
		if bodies {
			assert.Equal(t, "the secret prompt", got)
		} else {
			assert.NotEqual(t, "the secret prompt", got)
		}
	}
}

// The log must say what actually ran, not just "bash". The command
// rides through Body like any content.
func TestWatch_RecordsTheCommand(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("c", logging.WithDir(dir), logging.WithLevel("debug"), logging.WithBodies(true))
	require.NoError(t, err)

	bus := event.New()
	stop := logging.Watch(bus)
	call := uuid.Must(uuid.NewV7())
	args := map[string]any{"command": "rm -rf build"}
	bus.Publish(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: args})
	bus.Publish(event.ApprovalAsked{ToolCall: call, Tool: "bash", Args: args, Rationale: "recursive delete"})

	bus.Settle(2 * time.Second)
	require.Len(t, records(t, dir, "c"), 2)
	stop()
	require.NoError(t, closer())

	by := byEvent(t, records(t, dir, "c"))
	assert.Equal(t, "rm -rf build", by["tool_call.proposed"]["command"])
	assert.Equal(t, "rm -rf build", by["tool_call.approval"]["command"],
		"this record exists because a human read that string")
	assert.Equal(t, "recursive delete", by["tool_call.approval"][logging.KeyReason])
}

// slog writes its own "level", so a second one is not an extra field:
// it silently replaces the first for every reader.
func TestWatch_NeverWritesTwoLevelKeys(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("n", logging.WithDir(dir), logging.WithLevel("debug"))
	require.NoError(t, err)

	bus := event.New()
	stop := logging.Watch(bus)
	bus.Publish(event.Notice{Level: "info", Text: "named test"})

	bus.Settle(2 * time.Second)
	require.Len(t, records(t, dir, "n"), 1)
	stop()
	require.NoError(t, closer())

	raw, err := os.ReadFile(filepath.Join(dir, "n.jsonl"))
	require.NoError(t, err)
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		assert.Equal(t, 1, strings.Count(line, `"level":`), "duplicate level key: %s", line)
	}
	assert.Equal(t, "INFO", records(t, dir, "n")[0]["level"])
	assert.Equal(t, "info", records(t, dir, "n")[0]["severity"])
}

// testID makes a deterministic uuid from a readable name.
func testID(name string) uuid.UUID { return uuid.NewSHA1(uuid.Nil, []byte(name)) }
