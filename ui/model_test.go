package ui

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// Output is one event per line, so a loud Call would otherwise cost
// one full render per line.
func TestNextFact_DrainsWhatIsAlreadyQueued(t *testing.T) {
	bus := event.New()
	facts, stop := bus.Subscribe(event.Facts())
	defer stop()

	// Widened so this asserts on batching, not on scheduler timing.
	defer widenWindow()()

	call := uuid.Must(uuid.NewV7())
	for i := range 40 {
		bus.Publish(event.OutputChunk{ToolCall: call, Line: string(rune('a' + i%26))})
	}

	msg, ok := nextFact(facts)().(factMsg)
	require.True(t, ok)
	assert.Len(t, msg.events, 40, "one render should absorb the whole burst")
}

func TestNextFact_BatchIsBounded(t *testing.T) {
	bus := event.New()
	facts, stop := bus.Subscribe(event.Facts())
	defer stop()

	defer widenWindow()()

	call := uuid.Must(uuid.NewV7())
	for range maxFactBatch + 50 {
		bus.Publish(event.OutputChunk{ToolCall: call, Line: "x"})
	}

	msg := nextFact(facts)().(factMsg)
	assert.Len(t, msg.events, maxFactBatch, "a loud Call must not starve keystrokes")
}

// A closed bus has to end the pump, or Update re-arms forever.
func TestNextFact_StopsWhenTheBusCloses(t *testing.T) {
	bus := event.New()
	facts, _ := bus.Subscribe(event.Facts())
	bus.Close()
	assert.Nil(t, nextFact(facts)())
}

// Ticking per fact restarts the spinner chain and costs a frame, so
// it may only happen on the false to true edge.
func TestFacts_TickTheSpinnerOnlyWhenWaitingBegins(t *testing.T) {
	m := New(t.Context(), event.New(), SessionInfo{})
	m.layout.width, m.layout.height = 120, 40
	turn := uuid.Must(uuid.NewV7())

	_, cmd := m.route(factMsg{[]event.Event{event.TurnStarted{Turn: turn, N: 1, Prompt: "go"}}})
	require.NotNil(t, cmd, "waiting just began, the spinner has to start")

	m.waiting = true
	_, cmd = m.route(factMsg{[]event.Event{event.OutputChunk{ToolCall: uuid.Nil, Line: "x"}}})
	require.NotNil(t, cmd, "the pump always re-arms")
}

// widenWindow makes coalescing wait long enough to assert on size.
func widenWindow() func() {
	was := coalesceWindow
	coalesceWindow = 500 * time.Millisecond
	return func() { coalesceWindow = was }
}
