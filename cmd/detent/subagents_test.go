package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/config"
	"github.com/vitzeno/detent/internal/model"
)

// spawn_agent without a child model could only say there are none, and a
// child model without the tool is never used, so they come together or not.
func TestSubagents_TheToolAndTheChildModelComeTogether(t *testing.T) {
	var built []*model.Client
	child := func(opts ...model.ClientOption) *model.Client {
		c := model.NewClient("", "", "", opts...)
		built = append(built, c)
		return c
	}

	off, err := subagents(config.Config{}, child)
	require.NoError(t, err)
	assert.Empty(t, off.tools)
	assert.Empty(t, off.engine)
	assert.Empty(t, off.root)
	assert.Empty(t, built)

	yes := true
	on, err := subagents(config.Config{Subagents: &yes}, child)
	require.NoError(t, err)
	require.Len(t, on.tools, 1)
	assert.Equal(t, event.ToolSpawnAgent, on.tools[0].Name())
	assert.Len(t, on.engine, 3, "the child model, its limits and the reviewer")
	assert.Len(t, on.root, 1, "the root is told it may spawn")
	require.Len(t, built, 2)
	assert.Equal(t, "reviewer", built[1].PromptParts()[0].Name, "the reviewer has its own prompt")
}

func TestSubagents_ABadTimeoutStopsStartup(t *testing.T) {
	yes := true
	_, err := subagents(config.Config{Subagents: &yes, ChildTimeout: "never"},
		func(...model.ClientOption) *model.Client { return nil })
	assert.ErrorContains(t, err, "child_timeout")
}
