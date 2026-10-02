package host

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShell_DeliversLinesAndResult(t *testing.T) {
	res, err, events := runCollect(context.Background(), "echo out; echo err >&2; echo two")
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

func TestShell_NilChannelSkipsEvents(t *testing.T) {
	res, err := NewShell().Run(context.Background(), "echo hi", nil)
	require.NoError(t, err)
	assert.Equal(t, "hi\n", res.Stdout)
}

func TestShell_NonZeroExitIsAResult(t *testing.T) {
	res, err, events := runCollect(context.Background(), "echo before; exit 3")
	require.NoError(t, err)
	assert.Equal(t, 3, res.ExitCode)
	assert.Len(t, events, 1)
}

func TestShell_PartialFinalLineDelivered(t *testing.T) {
	res, err, events := runCollect(context.Background(), "printf 'nonl'")
	require.NoError(t, err)
	assert.Equal(t, "nonl\n", res.Stdout)
	require.Len(t, events, 1)
	assert.Equal(t, "nonl", events[0].Line)
}

func TestShell_OutputBounded(t *testing.T) {
	res, err, events := runCollect(context.Background(), "yes | head -c 100000")
	require.NoError(t, err)
	assert.True(t, res.Truncated)
	assert.LessOrEqual(t, len(res.Stdout), MaxOutputBytes)
	assert.Greater(t, len(events), 0)
}

func TestShell_EmptyCommandRejected(t *testing.T) {
	_, err := NewShell().Run(context.Background(), "  ", nil)
	assert.ErrorContains(t, err, "empty command")
}

func TestShell_ContextTimeoutWins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	// tail blocks everywhere, while sleep is a no-op shim in some sandboxes.
	_, err := NewShell().Run(ctx, "tail -f /dev/null", nil)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "killed") ||
		strings.Contains(err.Error(), "signal") ||
		strings.Contains(err.Error(), "deadline") ||
		strings.Contains(err.Error(), "canceled"))
}

// runCollect runs command and drains events on a separate goroutine, so
// a full channel never blocks the scanners mid-command.
func runCollect(ctx context.Context, command string) (Result, error, []StreamEvent) {
	ch := make(chan StreamEvent, 64)
	var events []StreamEvent
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range ch {
			events = append(events, e)
		}
	}()
	res, err := NewShell().Run(ctx, command, ch)
	<-done
	return res, err, events
}
