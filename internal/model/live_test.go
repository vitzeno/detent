package model

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The go/no-go for the rewrite. Every other test here proves detent
// speaks the protocol; this one proves the endpoint does, which is the
// single external unknown in the plan.
//
// Run it against whatever you actually use:
//
//	DETENT_LIVE=1 DETENT_BASE_URL=... DETENT_MODEL=... DETENT_API_KEY=... \
//	  go test ./internal/model/ -run TestLive -v
func TestLive_ToolCallsRoundTrip(t *testing.T) {
	if os.Getenv("DETENT_LIVE") == "" {
		t.Skip("set DETENT_LIVE=1 to run against a real endpoint")
	}
	c := &Client{
		BaseURL: os.Getenv("DETENT_BASE_URL"),
		Model:   os.Getenv("DETENT_MODEL"),
		APIKey:  os.Getenv("DETENT_API_KEY"),
		Env:     LocalEnvironment(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, Ping(ctx, c.baseURL(), c.APIKey))

	tools := []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name":        "list_dir",
			"description": "List a directory's contents.",
			"parameters": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"path": map[string]any{"type": "string", "description": "directory"}},
				"required":             []string{"path"},
				"additionalProperties": false,
			},
		},
	}}

	// 1. Does it call a tool at all?
	first, used, err := c.Complete(ctx, []Message{
		{Role: RoleUser, Content: "List what is in the current directory. Use the tool."},
	}, tools)
	require.NoError(t, err)
	t.Logf("step 1: %d calls, %d tokens, %v, finish=%q", len(first.Calls), used.Tokens(), used.Latency, first.Stop)
	require.NotEmpty(t, first.Calls, "the endpoint never called a tool; this model cannot drive the loop")

	call := first.Calls[0]
	assert.Equal(t, "list_dir", call.Name)
	assert.NotEmpty(t, call.ID, "an id is what the answer pairs to")
	assert.Empty(t, call.Err, "arguments did not parse: %s", call.Err)

	// 2. Does it accept the answer back? This is the half the old
	// adapter avoided by sending tool results as role "user".
	second, used2, err := c.Complete(ctx, []Message{
		{Role: RoleUser, Content: "List what is in the current directory. Use the tool."},
		{Role: RoleAssistant, Content: first.Text, Calls: first.Calls},
		Answer(call, "go.mod\ngo.sum\nREADME.md"),
	}, tools)
	require.NoError(t, err, "the endpoint rejected a tool result; see the compatibility note in AGENT_PLAN.md")
	t.Logf("step 2: %d calls, %d tokens, %v, finish=%q", len(second.Calls), used2.Tokens(), used2.Latency, second.Stop)
	assert.NotEmpty(t, second.Text+joinNames(second.Calls), "the endpoint answered with nothing at all")
}

func joinNames(calls []ToolCall) string {
	var s string
	for _, c := range calls {
		s += c.Name
	}
	return s
}
