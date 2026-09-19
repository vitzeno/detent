package shell

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_Echo(t *testing.T) {
	res, err := Run(context.Background(), "echo hello")
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "hello\n", res.Stdout)
	assert.Empty(t, res.Stderr)
	assert.False(t, res.Truncated)
}

func TestRun_FailingCommandIsAResultNotAnError(t *testing.T) {
	res, err := Run(context.Background(), "ls /nonexistent-detent-dir")
	require.NoError(t, err)
	assert.NotEqual(t, 0, res.ExitCode)
	assert.NotEmpty(t, res.Stderr)
}

func TestRun_StderrCapturedSeparately(t *testing.T) {
	res, err := Run(context.Background(), "echo out; echo err >&2")
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "out\n", res.Stdout)
	assert.Equal(t, "err\n", res.Stderr)
}

func TestRun_EmptyCommandRejected(t *testing.T) {
	_, err := Run(context.Background(), "   ")
	assert.ErrorContains(t, err, "empty command")
}

func TestRun_OutputBounded(t *testing.T) {
	res, err := Run(context.Background(), "yes | head -c 100000")
	require.NoError(t, err)
	assert.True(t, res.Truncated)
	assert.LessOrEqual(t, len(res.Stdout), MaxOutputBytes)
}

func TestRun_ContextTimeoutWins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	// tail blocks everywhere; sleep is a no-op shim in some sandboxes.
	_, err := Run(ctx, "tail -f /dev/null")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "killed") ||
		strings.Contains(err.Error(), "signal") ||
		strings.Contains(err.Error(), "deadline") ||
		strings.Contains(err.Error(), "canceled"))
}

func TestResult_Summary(t *testing.T) {
	assert.Equal(t, "exit 0, 2 lines", Result{Stdout: "a\nb\n"}.Summary())
	assert.Contains(t, Result{Stdout: "a\n", Truncated: true}.Summary(), "truncated")
}
