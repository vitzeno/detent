package logging_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/logging"
)

// The whole of how a session gets logged: facts in, records out, with
// correlation off the event rather than threaded through call sites.
func TestWatch_LogsEveryFactWithItsIDs(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup(logging.Options{Dir: dir, Session: "w1", Level: "debug"})
	require.NoError(t, err)

	bus := event.New()
	stop := logging.Watch(bus)

	turn, step, call := event.NewID(), event.NewID(), event.NewID()
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

	assert.Equal(t, string(turn), by["turn.started"][logging.KeyTurn])
	assert.Equal(t, string(step), by["step.started"][logging.KeyStep])
	assert.Equal(t, string(call), by["call.proposed"][logging.KeyCall],
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

// Live output is the one thing too noisy to keep, and CallEnded
// carries the whole of it anyway.
func TestWatch_SkipsLiveOutput(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup(logging.Options{Dir: dir, Session: "w2", Level: "debug"})
	require.NoError(t, err)

	bus := event.New()
	stop := logging.Watch(bus)
	for range 50 {
		bus.Publish(event.OutputChunk{Call: "c1", Line: "noise"})
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
		closer, err := logging.Setup(logging.Options{
			Dir: dir, Session: "b", Level: "debug", Bodies: bodies})
		require.NoError(t, err)

		bus := event.New()
		stop := logging.Watch(bus)
		bus.Publish(event.TurnStarted{Turn: "t", N: 1, Prompt: "the secret prompt"})

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
