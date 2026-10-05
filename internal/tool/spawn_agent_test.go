package tool

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// A spawn reads by itself and names no executor: the UI and undo read an
// executor as work outside the sandbox, which a child that only reads is not.
func TestSpawnAgent_DelegatesAndOnlyReads(t *testing.T) {
	c, err := Standard(SpawnAgent{}).Prepare(SpawnAgentName, map[string]any{"task": "find the session code", "name": nil})
	require.NoError(t, err)
	assert.True(t, c.Delegates)
	assert.Equal(t, event.MutRead, c.Mutability)
	assert.Empty(t, c.Executor)
	assert.Contains(t, c.Command, "find the session code", "what a human would read")
}

func TestSpawnAgent_NeedsATask(t *testing.T) {
	_, err := Standard(SpawnAgent{}).Prepare(SpawnAgentName, map[string]any{"name": "x"})
	assert.ErrorContains(t, err, "task")
}
