package shell

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStream_DeliversLinesAndResult(t *testing.T) {
	var events []StreamEvent
	res, err := Stream(context.Background(), "echo out; echo err >&2; echo two", func(e StreamEvent) {
		events = append(events, e)
	})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "out\ntwo\n", res.Stdout)
	assert.Equal(t, "err\n", res.Stderr)

	require.Len(t, events, 3)
	// Stderr interleaving is nondeterministic, so assert the set, not the sequence.
	assert.ElementsMatch(t, []StreamEvent{
		{Line: "out"},
		{Stderr: true, Line: "err"},
		{Line: "two"},
	}, events)
}

func TestStream_NilCallbackBehavesLikeRun(t *testing.T) {
	res, err := Stream(context.Background(), "echo hi", nil)
	require.NoError(t, err)
	assert.Equal(t, "hi\n", res.Stdout)
}

func TestStream_NonZeroExitIsAResult(t *testing.T) {
	var n int
	res, err := Stream(context.Background(), "echo before; exit 3", func(StreamEvent) { n++ })
	require.NoError(t, err)
	assert.Equal(t, 3, res.ExitCode)
	assert.Equal(t, 1, n)
}

func TestStream_PartialFinalLineDelivered(t *testing.T) {
	var events []StreamEvent
	res, err := Stream(context.Background(), "printf 'nonl'", func(e StreamEvent) {
		events = append(events, e)
	})
	require.NoError(t, err)
	assert.Equal(t, "nonl\n", res.Stdout)
	require.Len(t, events, 1)
	assert.Equal(t, "nonl", events[0].Line)
}

func TestStream_OutputBounded(t *testing.T) {
	var n int
	res, err := Stream(context.Background(), "yes | head -c 100000", func(StreamEvent) { n++ })
	require.NoError(t, err)
	assert.True(t, res.Truncated)
	assert.LessOrEqual(t, len(res.Stdout), MaxOutputBytes)
	assert.Greater(t, n, 0)
}

func TestStream_EmptyCommandRejected(t *testing.T) {
	_, err := Stream(context.Background(), "  ", nil)
	assert.ErrorContains(t, err, "empty command")
}
