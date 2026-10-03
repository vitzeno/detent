package model

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// Proves the endpoint does tool calling, using .detent.yaml like the harness:
//
//	DETENT_LIVE=1 go test ./internal/model/ -run TestLive -v
func TestLive_ToolCallsRoundTrip(t *testing.T) {
	if os.Getenv("DETENT_LIVE") == "" {
		t.Skip("set DETENT_LIVE=1 to run against a real endpoint")
	}
	cfg := liveConfig(t)
	c := &Client{
		BaseURL: cfg["base_url"],
		Model:   cfg["model"],
		APIKey:  cfg["api_key"],
		Env:     LocalEnvironment(),
	}
	t.Logf("endpoint %s, model %s", c.baseURL(), c.model())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, c.Ping(ctx))

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
	first, used, err := c.Complete(ctx, []event.Message{
		{Role: event.RoleUser, Content: "List what is in the current directory. Use the tool."},
	}, tools)
	require.NoError(t, err)
	t.Logf("step 1: %d calls, %d tokens, %v, finish=%q", len(first.Calls), used.Tokens(), used.Latency, first.Stop)
	require.NotEmpty(t, first.Calls, "the endpoint never called a tool; this model cannot drive the loop")

	call := first.Calls[0]
	assert.Equal(t, "list_dir", call.Name)
	assert.NotEmpty(t, call.ID, "an id is what the answer pairs to")
	assert.Empty(t, call.Err, "arguments did not parse: %s", call.Err)

	// 2. Does it accept a role "tool" answer back?
	second, used2, err := c.Complete(ctx, []event.Message{
		{Role: event.RoleUser, Content: "List what is in the current directory. Use the tool."},
		{Role: event.RoleAssistant, Content: first.Text, Calls: first.Calls},
		event.Answer(call, "go.mod\ngo.sum\nREADME.md"),
	}, tools)
	require.NoError(t, err, "the endpoint rejected a tool result")
	t.Logf("step 2: %d calls, %d tokens, %v, finish=%q", len(second.Calls), used2.Tokens(), used2.Latency, second.Stop)
	assert.NotEmpty(t, second.Text+joinNames(second.Calls), "the endpoint answered with nothing at all")
}

// liveConfig reads the few keys this test needs out of .detent.yaml,
// with DETENT_* env vars winning, without importing internal/config.
func liveConfig(t *testing.T) map[string]string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)

	var raw []byte
	for range 4 {
		if b, err := os.ReadFile(filepath.Join(dir, ".detent.yaml")); err == nil {
			raw = b
			break
		}
		dir = filepath.Dir(dir)
	}
	require.NotNil(t, raw, "no .detent.yaml found above the test directory")

	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v, _, _ = strings.Cut(v, " #")
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	for _, k := range []string{"base_url", "model", "api_key"} {
		if env := os.Getenv("DETENT_" + strings.ToUpper(k)); env != "" {
			out[k] = env
		}
	}
	return out
}

func joinNames(calls []event.ToolCall) string {
	var s string
	for _, c := range calls {
		s += c.Name
	}
	return s
}
