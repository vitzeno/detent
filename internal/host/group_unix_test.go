//go:build !windows

package host

import (
	"context"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A cancel stops what sh started too, not only sh, or it keeps running on the host.
func TestShell_CancelKillsTheWholeProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	res, err := NewShell().Run(ctx, "sleep 30 & echo $!; wait", nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	pid, convErr := strconv.Atoi(strings.TrimSpace(res.Stdout))
	require.NoError(t, convErr, "partial output comes back with the error")

	assert.Eventually(t, func() bool { return syscall.Kill(pid, 0) != nil },
		2*time.Second, 20*time.Millisecond, "the backgrounded child outlived the cancel")
}
