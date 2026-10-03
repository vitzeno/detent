package host

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capture"
)

func TestShell_DeliversLinesAndResult(t *testing.T) {
	res, events, err := runCollect(context.Background(), "echo out; echo err >&2; echo two")
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "out\ntwo\n", res.Stdout)
	assert.Equal(t, "err\n", res.Stderr)

	require.Len(t, events, 3)
	// Stderr interleaving is nondeterministic, so assert the set, not the sequence.
	assert.ElementsMatch(t, []capture.StreamEvent{
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
	res, events, err := runCollect(context.Background(), "echo before; exit 3")
	require.NoError(t, err)
	assert.Equal(t, 3, res.ExitCode)
	assert.Len(t, events, 1)
}

func TestShell_PartialFinalLineDelivered(t *testing.T) {
	res, events, err := runCollect(context.Background(), "printf 'nonl'")
	require.NoError(t, err)
	assert.Equal(t, "nonl\n", res.Stdout)
	require.Len(t, events, 1)
	assert.Equal(t, "nonl", events[0].Line)
}

func TestShell_OutputBounded(t *testing.T) {
	res, events, err := runCollect(context.Background(), "yes | head -c 100000")
	require.NoError(t, err)
	assert.True(t, res.Truncated)
	assert.LessOrEqual(t, len(res.Stdout), capture.MaxOutputBytes)
	assert.NotEmpty(t, events)
}

func TestShell_ABackgroundedChildDoesNotHoldTheResult(t *testing.T) {
	start := time.Now()
	res, err := NewShell().Run(context.Background(), "echo hi; sleep 20 &", nil)
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "hi\n", res.Stdout)
	assert.Less(t, time.Since(start), 10*time.Second)
}

func TestShell_EmptyCommandRejected(t *testing.T) {
	_, err := NewShell().Run(context.Background(), "  ", nil)
	assert.ErrorIs(t, err, ErrEmptyCommand)
}

func TestShell_ContextTimeoutWins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	// tail blocks everywhere, while sleep is a no-op shim in some sandboxes.
	_, err := NewShell().Run(ctx, "tail -f /dev/null", nil)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// runCollect runs command and drains events on a separate goroutine, so
// a full channel never blocks the scanners mid-command.
func runCollect(ctx context.Context, command string) (capture.Result, []capture.StreamEvent, error) {
	ch := make(chan capture.StreamEvent, 64)
	var events []capture.StreamEvent
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range ch {
			events = append(events, e)
		}
	}()
	res, err := NewShell().Run(ctx, command, ch)
	close(ch)
	<-done
	return res, events, err
}

// A cancel stops what sh started too, not only sh, or it keeps running on the host.
func TestShell_CancelKillsTheWholeProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no process groups")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	res, err := NewShell().Run(ctx, "sleep 30 & echo $!; wait", nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	pid, convErr := strconv.Atoi(strings.TrimSpace(res.Stdout))
	require.NoError(t, convErr, "partial output comes back with the error")

	assert.Eventually(t, func() bool { return syscall.Kill(pid, 0) != nil },
		2*time.Second, 20*time.Millisecond, "the backgrounded child outlived the cancel")
}

func TestShell_CommandsDoNotSeeDetentsSecrets(t *testing.T) {
	t.Setenv("DETENT_API_KEY", "sk-detent")
	t.Setenv("TYPESAFE_API_KEY", "sk-jev")
	t.Setenv("DETENT_KEEP", "visible")
	res, err := NewShell().Run(t.Context(), "echo \"[$DETENT_API_KEY][$TYPESAFE_API_KEY][$DETENT_KEEP]\"", nil)
	require.NoError(t, err)
	assert.Equal(t, "[][][visible]\n", res.Stdout)
}

func TestShell_WithLimitTakesEffect(t *testing.T) {
	res, err := NewShell(WithLimit(4)).Run(t.Context(), "echo abcdefgh", nil)
	require.NoError(t, err)
	assert.Equal(t, "abcd", res.Stdout)
	assert.True(t, res.Truncated)
}

func TestShell_ZeroValueUsesTheDefaultLimit(t *testing.T) {
	res, err := (&Shell{}).Run(t.Context(), "echo hi", nil)
	require.NoError(t, err)
	assert.Equal(t, "hi\n", res.Stdout)
	assert.False(t, res.Truncated)
}
