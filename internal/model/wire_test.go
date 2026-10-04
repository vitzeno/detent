package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// A Step on the wire: one assistant message carrying tool_calls, then
// one tool message per id. Endpoints reject either half alone.
func TestEncode_AStepIsAnAssistantPlusItsAnswers(t *testing.T) {
	call := event.ToolRequest{ID: "c1", Name: "read_file", Args: map[string]any{"path": "a.go"}}
	step := []event.Message{
		{Role: event.RoleUser, Content: "read a.go"},
		{Role: event.RoleAssistant, Content: "reading", Requests: []event.ToolRequest{call}},
		event.Answer(call, "package main"),
	}

	c, got := serve(t, `{"choices":[{"message":{"content":"done"}}]}`)
	_, _, err := c.Complete(context.Background(), step, nil)
	require.NoError(t, err)

	msgs := (*got)["messages"].([]any)
	require.Len(t, msgs, 4, "the system prompt plus the three given")

	assistant := msgs[2].(map[string]any)
	assert.Equal(t, "assistant", assistant["role"])
	calls := assistant["tool_calls"].([]any)
	require.Len(t, calls, 1)
	fn := calls[0].(map[string]any)
	assert.Equal(t, "c1", fn["id"])
	assert.Equal(t, "function", fn["type"])
	args, ok := fn["function"].(map[string]any)["arguments"].(string)
	require.True(t, ok, "arguments is a JSON string, not an object")
	assert.JSONEq(t, `{"path":"a.go"}`, args)

	answer := msgs[3].(map[string]any)
	assert.Equal(t, "tool", answer["role"])
	assert.Equal(t, "c1", answer["tool_call_id"], "the id is what pairs it to the call")
	assert.Equal(t, "package main", answer["content"])
}

// Only a tool message carries tool_call_id, and only an assistant one
// tool_calls. A stray field makes endpoints reject the whole transcript.
func TestEncode_FieldsStayOnTheirOwnRole(t *testing.T) {
	c, got := serve(t, `{"choices":[{"message":{"content":"ok"}}]}`)
	_, _, err := c.Complete(context.Background(), []event.Message{
		{Role: event.RoleUser, Content: "hi"},
		{Role: event.RoleAssistant, Content: "prose, no calls"},
	}, nil)
	require.NoError(t, err)

	for _, m := range (*got)["messages"].([]any) {
		msg := m.(map[string]any)
		assert.NotContains(t, msg, "tool_calls", "%v", msg["role"])
		assert.NotContains(t, msg, "tool_call_id", "%v", msg["role"])
	}
}

func TestComplete_SendsToolsAndTheSystemPrompt(t *testing.T) {
	schemas := []map[string]any{{"type": "function", "function": map[string]any{"name": "bash"}}}
	c, got := serve(t, `{"choices":[{"message":{"content":"ok"}}]}`, WithEnvironment(
		Environment{OS: "linux", Arch: "arm64", Dir: "/workspace", Sandboxed: true, Undoable: true}))

	_, _, err := c.Complete(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}}, schemas)
	require.NoError(t, err)

	assert.Len(t, (*got)["tools"], 1)
	sys := (*got)["messages"].([]any)[0].(map[string]any)
	assert.Equal(t, "system", sys["role"])

	text := sys["content"].(string)
	assert.Contains(t, text, "linux/arm64", "the prompt must describe where commands run")
	assert.Contains(t, text, "/workspace")
	assert.Contains(t, text, "container")
	assert.Contains(t, text, "undo the whole request", "Undoable is per Turn")
}

// Without tools the request must not carry an empty array: some
// endpoints treat its presence as a demand to call something.
func TestComplete_OmitsToolsWhenThereAreNone(t *testing.T) {
	c, got := serve(t, `{"choices":[{"message":{"content":"ok"}}]}`)
	_, _, err := c.Complete(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}}, nil)
	require.NoError(t, err)
	assert.NotContains(t, *got, "tools")
}
