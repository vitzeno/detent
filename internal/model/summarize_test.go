package model

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
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

// Reasoning or a cut-off reply would replace the dropped Steps with noise.
func TestSummarize_RefusesAnUnfinishedReply(t *testing.T) {
	for _, body := range []string{
		`{"choices":[{"message":{"content":"","reasoning":"let me think"}}]}`,
		`{"choices":[{"message":{"content":"the build"},"finish_reason":"length"}]}`,
	} {
		c, _ := serve(t, body)
		out, err := c.Summarize(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}})
		require.Error(t, err)
		assert.Empty(t, out)
	}
}

func TestSummarize_CutsOnARuneBoundary(t *testing.T) {
	huge := "x" + strings.Repeat("é", MaxSummaryBytes)
	c, _ := serve(t, `{"choices":[{"message":{"content":"`+huge+`"}}]}`)
	out, err := c.Summarize(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}})
	require.NoError(t, err)
	assert.True(t, utf8.ValidString(out))
	assert.LessOrEqual(t, len(out), MaxSummaryBytes)
}

// The same history must read the same each time, and a written file's
// content is not what a summary needs.
func TestTranscriptText_BoundsArgumentsAndResults(t *testing.T) {
	call := event.ToolCall{ID: "c1", Name: "write_file",
		Args: map[string]any{"path": "a.go", "content": strings.Repeat("y", 5000)}}
	text := transcriptText([]event.Message{
		{Role: event.RoleAssistant, Calls: []event.ToolCall{call}},
		event.Answer(call, "start"+strings.Repeat("z", 10000)+"the error"),
	})
	assert.Contains(t, text, "write_file(content=yyy")
	assert.Less(t, strings.Index(text, "content="), strings.Index(text, "path=a.go"), "keys are sorted")
	assert.Less(t, len(text), maxResultBytes+maxArgBytes+200)
	assert.Contains(t, text, "the error", "the tail of a result is kept")
}

func TestSummarize_EmptyIsNotACall(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1"} // would fail if dialled
	out, err := c.Summarize(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, out)
}
