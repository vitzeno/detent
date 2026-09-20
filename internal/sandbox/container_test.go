package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capture"
)

// containerd.New prepends "unix://" itself; the address it takes is a
// bare path, not a URI. Colima's default profile always exposes it
// under the current user's home directory.
var testSocket = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".colima", "default", "containerd.sock")
}()

// daemonAvailable is cached so every test pays the connection cost once.
var daemonAvailable = sync.OnceValue(func() bool {
	if testSocket == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return Preflight(ctx, testSocket) == nil
})

func newTestContainer(t *testing.T) *Container {
	t.Helper()
	if !daemonAvailable() {
		t.Skip("containerd not reachable at", testSocket)
	}
	c := NewContainer(WithSocket(testSocket), WithNamespace("detent-test"))
	id := strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	require.NoError(t, c.Start(context.Background(), id))
	t.Cleanup(func() {
		_ = c.Close(context.Background())
	})
	return c
}

func TestContainer_RunCapturesOutputAndExitCode(t *testing.T) {
	c := newTestContainer(t)

	res, err := c.Run(context.Background(), "echo out; echo err >&2; exit 3", nil)
	require.NoError(t, err)
	assert.Equal(t, "out\n", res.Stdout)
	assert.Equal(t, "err\n", res.Stderr)
	assert.Equal(t, 3, res.ExitCode)
}

func TestContainer_StatePersistsAcrossRuns(t *testing.T) {
	c := newTestContainer(t)
	ctx := context.Background()

	_, err := c.Run(ctx, "echo hello > /workspace-marker.txt", nil)
	require.NoError(t, err)

	res, err := c.Run(ctx, "cat /workspace-marker.txt", nil)
	require.NoError(t, err)
	assert.Equal(t, "hello\n", res.Stdout, "state from the first Run must persist into the second")
}

func TestContainer_WorkspaceMountParity(t *testing.T) {
	c := newTestContainer(t)
	ctx := context.Background()

	wd, err := os.Getwd()
	require.NoError(t, err)
	marker := filepath.Join(wd, ".sandbox-test-marker")
	require.NoError(t, os.WriteFile(marker, []byte("host-written\n"), 0o644))
	t.Cleanup(func() { os.Remove(marker) })

	res, err := c.Run(ctx, "cat /workspace/.sandbox-test-marker", nil)
	require.NoError(t, err)
	assert.Equal(t, "host-written\n", res.Stdout, "a host-written file must be visible in the container's mount")

	_, err = c.Run(ctx, "echo container-written > /workspace/.sandbox-test-marker2", nil)
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(wd, ".sandbox-test-marker2"))
	require.NoError(t, err)
	assert.Equal(t, "container-written\n", string(got), "a container-written file must be visible on the host")
	os.Remove(filepath.Join(wd, ".sandbox-test-marker2"))
}

func TestContainer_StreamsLiveEvents(t *testing.T) {
	c := newTestContainer(t)

	ch := make(chan capture.StreamEvent, 16)
	res, err := c.Run(context.Background(), "echo one; echo two", ch)
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\n", res.Stdout)

	var lines []string
	for e := range ch {
		lines = append(lines, e.Line)
	}
	assert.Equal(t, []string{"one", "two"}, lines)
}

func TestContainer_SnapshotAndRollback(t *testing.T) {
	c := newTestContainer(t)
	ctx := context.Background()

	_, err := c.Run(ctx, "echo before > /rollback-marker.txt", nil)
	require.NoError(t, err)

	checkpoint, err := c.Snapshot(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, checkpoint)

	_, err = c.Run(ctx, "echo after > /rollback-marker.txt", nil)
	require.NoError(t, err)
	res, err := c.Run(ctx, "cat /rollback-marker.txt", nil)
	require.NoError(t, err)
	require.Equal(t, "after\n", res.Stdout, "state must reflect the second write before rolling back")

	require.NoError(t, c.Rollback(ctx, checkpoint))

	res, err = c.Run(ctx, "cat /rollback-marker.txt", nil)
	require.NoError(t, err)
	assert.Equal(t, "before\n", res.Stdout, "rollback must restore the checkpointed state")

	_, err = c.Run(ctx, "echo new-branch >> /rollback-marker.txt", nil)
	require.NoError(t, err, "the container must still be usable for new commands after rollback")
}

// TestContainer_RollbackLeavesTheWorkspaceAlone pins a real limit of
// snapshot-based rollback: the workspace is a bind mount to the host,
// not part of the snapshot, so edits to the user's own files survive
// a rollback. Everything outside the mount is restored.
func TestContainer_RollbackLeavesTheWorkspaceAlone(t *testing.T) {
	c := newTestContainer(t)
	ctx := context.Background()

	_, err := c.Run(ctx, "echo before > /workspace/.rbtest; echo before > /outside.txt", nil)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = c.Run(context.Background(), "rm -f /workspace/.rbtest", nil) })

	checkpoint, err := c.Snapshot(ctx)
	require.NoError(t, err)

	_, err = c.Run(ctx, "echo after > /workspace/.rbtest; echo after > /outside.txt", nil)
	require.NoError(t, err)
	require.NoError(t, c.Rollback(ctx, checkpoint))

	res, err := c.Run(ctx, "cat /outside.txt", nil)
	require.NoError(t, err)
	assert.Equal(t, "before\n", res.Stdout, "container state outside the mount is restored")

	res, err = c.Run(ctx, "cat /workspace/.rbtest", nil)
	require.NoError(t, err)
	assert.Equal(t, "after\n", res.Stdout, "bind-mounted workspace is not snapshotted, so it is not restored")
}

// TestContainer_RollbackTargetsTheRightCheckpoint mirrors the real
// loop: snapshot after every step, then roll back to an earlier one.
// Each checkpoint must hold exactly the state as of its own step, so
// an off-by-one in either direction fails here.
func TestContainer_RollbackTargetsTheRightCheckpoint(t *testing.T) {
	c := newTestContainer(t)
	ctx := context.Background()

	var checkpoints []string
	for _, step := range []string{"one", "two", "three"} {
		_, err := c.Run(ctx, "echo "+step+" >> /steps.txt", nil)
		require.NoError(t, err)
		id, err := c.Snapshot(ctx)
		require.NoError(t, err)
		checkpoints = append(checkpoints, id)
	}

	res, err := c.Run(ctx, "cat /steps.txt", nil)
	require.NoError(t, err)
	require.Equal(t, "one\ntwo\nthree\n", res.Stdout, "all three steps ran")

	// Checkpoint 1 was taken after step one, so it holds step one only.
	require.NoError(t, c.Rollback(ctx, checkpoints[0]))
	res, err = c.Run(ctx, "cat /steps.txt", nil)
	require.NoError(t, err)
	assert.Equal(t, "one\n", res.Stdout, "rolling back to checkpoint 1 keeps step one and drops two and three")

	// Checkpoint 2 holds steps one and two, and is still reachable
	// after having rolled back past it.
	require.NoError(t, c.Rollback(ctx, checkpoints[1]))
	res, err = c.Run(ctx, "cat /steps.txt", nil)
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\n", res.Stdout, "rolling back to checkpoint 2 keeps steps one and two")
}
