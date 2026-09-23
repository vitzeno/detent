package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A Step on the wire: one assistant message carrying tool_calls, then
// one tool message per id. Endpoints reject either half alone, so this
// is the shape the whole rewrite depends on.
func TestEncode_AStepIsAnAssistantPlusItsAnswers(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "read_file", Args: map[string]any{"path": "a.go"}}
	step := []Message{
		{Role: RoleUser, Content: "read a.go"},
		{Role: RoleAssistant, Content: "reading", Calls: []ToolCall{call}},
		Answer(call, "package main"),
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
	// Arguments is a JSON string, not an object. Every endpoint agrees
	// on this and none accepts the object.
	assert.Equal(t, `{"path":"a.go"}`, fn["function"].(map[string]any)["arguments"])

	answer := msgs[3].(map[string]any)
	assert.Equal(t, "tool", answer["role"])
	assert.Equal(t, "c1", answer["tool_call_id"], "the id is what pairs it to the call")
	assert.Equal(t, "package main", answer["content"])
}

// Only a tool message carries tool_call_id, and only an assistant
// message carries tool_calls. A stray field on the wrong role is how
// an endpoint starts rejecting the whole transcript.
func TestEncode_FieldsStayOnTheirOwnRole(t *testing.T) {
	c, got := serve(t, `{"choices":[{"message":{"content":"ok"}}]}`)
	_, _, err := c.Complete(context.Background(), []Message{
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "prose, no calls"},
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
	c, got := serve(t, `{"choices":[{"message":{"content":"ok"}}]}`)
	c.Env = Environment{OS: "linux", Arch: "arm64", Dir: "/workspace", Sandboxed: true, Undoable: true}

	_, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, schemas)
	require.NoError(t, err)

	assert.Len(t, (*got)["tools"], 1)
	sys := (*got)["messages"].([]any)[0].(map[string]any)
	assert.Equal(t, "system", sys["role"])

	text := sys["content"].(string)
	assert.Contains(t, text, "linux/arm64", "the prompt must describe where commands run")
	assert.Contains(t, text, "/workspace")
	assert.Contains(t, text, "container")
	assert.Contains(t, text, "undo the whole request", "Undoable is per Turn now")
}

// Without tools the request must not carry an empty array: some
// endpoints treat its presence as a demand to call something.
func TestComplete_OmitsToolsWhenThereAreNone(t *testing.T) {
	c, got := serve(t, `{"choices":[{"message":{"content":"ok"}}]}`)
	_, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, nil)
	require.NoError(t, err)
	assert.NotContains(t, *got, "tools")
}

func TestEnvironment_SaysWhatChanges(t *testing.T) {
	tests := []struct {
		name  string
		env   Environment
		wants []string
		nots  []string
	}{
		{
			name:  "unsandboxed says the files are real",
			env:   Environment{OS: "darwin", Arch: "arm64", Dir: "/x", Network: true},
			wants: []string{"own machine", "network is reachable"},
			nots:  []string{"container", "undo"},
		},
		{
			name:  "no network says so plainly",
			env:   Environment{OS: "linux", Arch: "amd64", Dir: "/x"},
			wants: []string{"no network", "Work with what is already here"},
			nots:  []string{"network is reachable"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := systemPrompt(tt.env)
			for _, w := range tt.wants {
				assert.Contains(t, got, w)
			}
			for _, n := range tt.nots {
				assert.NotContains(t, got, n)
			}
		})
	}
}

func TestLocalEnvironment_DescribesThisMachine(t *testing.T) {
	env := LocalEnvironment()
	assert.NotEmpty(t, env.OS)
	assert.NotEmpty(t, env.Dir)
	assert.True(t, env.Network)
	assert.False(t, env.Undoable, "nothing checkpoints the user's own filesystem")
	assert.False(t, env.Sandboxed)
}
