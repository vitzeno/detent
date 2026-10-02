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
	bus.Publish(event.CallProposed{Call: call, Step: step, Tool: "bash"})
	bus.Publish(event.CallEnded{Call: call, Result: event.Result{ExitCode: 1, Stderr: "boom"},
		Took: 12 * time.Millisecond})
	bus.Publish(event.TurnEnded{Turn: turn, Reason: event.EndDone})

	require.Eventually(t, func() bool { return len(records(t, dir, "w1")) == 5 },
		2*time.Second, 10*time.Millisecond)
	stop()
	require.NoError(t, closer())

	got := records(t, dir, "w1")
	by := map[string]map[string]any{}
	for _, r := range got {
		by[r[logging.KeyEvent].(string)] = r
	}

	assert.Equal(t, turn.String(), by["turn.started"][logging.KeyTurn])
	assert.Equal(t, step.String(), by["step.started"][logging.KeyStep])
	assert.Equal(t, call.String(), by["call.proposed"][logging.KeyCall],
		"a call record names its call, and nothing had to thread it")

	ended := by["call.ended"]
	assert.EqualValues(t, 1, ended["exit"])
	assert.EqualValues(t, 12, ended[logging.KeyMS])
	assert.Equal(t, "WARN", ended["level"], "a failed call is worth finding")

	for _, r := range got {
		assert.Equal(t, "w1", r[logging.KeySession])
		assert.NotZero(t, r[logging.KeyOrdinal], "an ordinal makes a gap visible")
	}
}

// A command the human ran is logged like a Call, under its own key:
// the id has to be findable, and a failure has to be worth finding.
func TestWatch_LogsAHumanCommand(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("w5", logging.WithDir(dir),
		logging.WithLevel("debug"), logging.WithBodies(true))
	require.NoError(t, err)

	bus := event.New()
	stop := logging.Watch(bus)

	shell := uuid.Must(uuid.NewV7())
	bus.Publish(event.ShellStarted{Shell: shell, Command: "git status", Runner: "sandbox"})
	bus.Publish(event.ShellEnded{Shell: shell, Took: 8 * time.Millisecond,
		Result: event.Result{ExitCode: 1, Stderr: "boom"}})

	require.Eventually(t, func() bool { return len(records(t, dir, "w5")) == 2 },
		2*time.Second, 10*time.Millisecond)
	stop()
	require.NoError(t, closer())

	by := map[string]map[string]any{}
	for _, r := range records(t, dir, "w5") {
		by[r[logging.KeyEvent].(string)] = r
	}

	started := by["shell.started"]
	assert.Equal(t, shell.String(), started[logging.KeyShell])
	assert.Equal(t, "git status", started["command"])
	assert.Equal(t, "sandbox", started["runner"])

	ended := by["shell.ended"]
	assert.Equal(t, shell.String(), ended[logging.KeyShell])
	assert.EqualValues(t, 1, ended["exit"])
	assert.EqualValues(t, 8, ended[logging.KeyMS])
	assert.Equal(t, "WARN", ended["level"], "a failed command is worth finding")
}

// Live output is the one thing too noisy to keep, and CallEnded
// carries the whole of it anyway.
func TestWatch_SkipsLiveOutput(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("w2", logging.WithDir(dir), logging.WithLevel("debug"))
	require.NoError(t, err)

	bus := event.New()
	stop := logging.Watch(bus)
	for range 50 {
		bus.Publish(event.OutputChunk{Call: testID("c1"), Line: "noise"})
	}
	bus.Publish(event.Notice{Level: "info", Text: "done"})

	require.Eventually(t, func() bool { return len(records(t, dir, "w2")) >= 1 },
		2*time.Second, 10*time.Millisecond)
	stop()
	require.NoError(t, closer())

	for _, r := range records(t, dir, "w2") {
		assert.NotEqual(t, "call.output", r[logging.KeyEvent])
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

		require.Eventually(t, func() bool { return len(records(t, dir, "b")) == 1 },
			2*time.Second, 10*time.Millisecond)
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
	bus.Publish(event.CallProposed{Call: call, Tool: "bash", Args: args})
	bus.Publish(event.ApprovalAsked{Call: call, Tool: "bash", Args: args, Rationale: "recursive delete"})

	require.Eventually(t, func() bool { return len(records(t, dir, "c")) == 2 },
		2*time.Second, 10*time.Millisecond)
	stop()
	require.NoError(t, closer())

	by := map[string]map[string]any{}
	for _, r := range records(t, dir, "c") {
		by[r[logging.KeyEvent].(string)] = r
	}
	assert.Equal(t, "rm -rf build", by["call.proposed"]["command"])
	assert.Equal(t, "rm -rf build", by["call.approval"]["command"],
		"this record exists because a human read that string")
	assert.Equal(t, "recursive delete", by["call.approval"][logging.KeyReason])
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

	require.Eventually(t, func() bool { return len(records(t, dir, "n")) == 1 },
		2*time.Second, 10*time.Millisecond)
	stop()
	require.NoError(t, closer())

	raw, err := os.ReadFile(filepath.Join(dir, "n.jsonl"))
	require.NoError(t, err)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		assert.Equal(t, 1, strings.Count(line, `"level":`), "duplicate level key: %s", line)
	}
	assert.Equal(t, "INFO", records(t, dir, "n")[0]["level"])
	assert.Equal(t, "info", records(t, dir, "n")[0]["severity"])
}

// testID makes a deterministic uuid from a readable name.
func testID(name string) uuid.UUID { return uuid.NewSHA1(uuid.Nil, []byte(name)) }
