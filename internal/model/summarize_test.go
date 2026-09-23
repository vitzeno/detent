package model

import (
	"context"
	"github.com/vitzeno/detent/event"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarize_FlattensStepsAndAsksForProse(t *testing.T) {
	call := event.ToolCall{ID: "c1", Name: "bash", Args: map[string]any{"command": "go test ./..."}}
	c, got := serve(t, `{"choices":[{"message":{"content":"  tests pass  "}}]}`)

	out, err := c.Summarize(context.Background(), []event.Message{
		{Role: event.RoleUser, Content: "run the tests"},
		{Role: event.RoleAssistant, Calls: []event.ToolCall{call}},
		event.Answer(call, "ok 12 packages"),
	})
	require.NoError(t, err)
	assert.Equal(t, "tests pass", out, "trimmed")

	assert.NotContains(t, *got, "tools", "a summary must not be able to call anything")
	msgs := (*got)["messages"].([]any)
	require.Len(t, msgs, 2)

	flat := msgs[1].(map[string]any)["content"].(string)
	assert.Contains(t, flat, "Human: run the tests")
	assert.Contains(t, flat, "Agent ran bash(command=go test ./...)")
	assert.Contains(t, flat, "Result: ok 12 packages")
}

func TestSummarize_BoundsWhatItReturns(t *testing.T) {
	huge := strings.Repeat("x", MaxSummaryBytes*2)
	c, _ := serve(t, `{"choices":[{"message":{"content":"`+huge+`"}}]}`)
	out, err := c.Summarize(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}})
	require.NoError(t, err)
	assert.Len(t, out, MaxSummaryBytes, "a summary that grew unbounded defeats the compaction asking for it")
}

func TestSummarize_EmptyIsNotACall(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1"} // would fail if dialled
	out, err := c.Summarize(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, out)
}
