package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/config"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// spawn_agent without a child model could only say there are none, and a
// child model without the tool is never used, so they come together or not.
func TestSubagents_TheToolAndTheChildModelComeTogether(t *testing.T) {
	built := 0
	child := func(opts ...model.ClientOption) *model.Client {
		built++
		return model.NewClient("", "", "", opts...)
	}

	off, err := subagents(config.Config{}, child)
	require.NoError(t, err)
	assert.Empty(t, off.tools)
	assert.Empty(t, off.engine)
	assert.Empty(t, off.root)
	assert.Zero(t, built)

	yes := true
	on, err := subagents(config.Config{Subagents: &yes}, child)
	require.NoError(t, err)
	require.Len(t, on.tools, 1)
	assert.Equal(t, tool.SpawnAgentName, on.tools[0].Name())
	assert.Len(t, on.engine, 2, "the child model and its limits")
	assert.Len(t, on.root, 1, "the root is told it may spawn")
	assert.Equal(t, 1, built)
}

func TestSubagents_ABadTimeoutStopsStartup(t *testing.T) {
	yes := true
	_, err := subagents(config.Config{Subagents: &yes, ChildTimeout: "never"},
		func(...model.ClientOption) *model.Client { return nil })
	assert.ErrorContains(t, err, "child_timeout")
}
