package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/leases"

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

// testContainerID is unique per run. Naming a container after its
// test alone lets a second run inherit the first's filesystem:
// containerd reclaims a dropped snapshot asynchronously, so the old
// name can still resolve. Only a test asserting on absolute state
// notices, which is why routing's rollback cover found it first.
func testContainerID(t *testing.T) string {
	t.Helper()
	var b [6]byte
	_, err := rand.Read(b[:])
	require.NoError(t, err)
	return strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-") + "-" + hex.EncodeToString(b[:])
}

func newTestContainer(t *testing.T) *Container {
	t.Helper()
	if !daemonAvailable() {
		t.Skip("containerd not reachable at", testSocket)
	}
	c := NewContainer(WithSocket(testSocket), WithNamespace("detent-test"))
	id := testContainerID(t)
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

// Rolling back orphans every later checkpoint. Unless they're leased,
// the next GC pass sweeps them and rollback works exactly once.
func TestContainer_CheckpointsSurviveGarbageCollection(t *testing.T) {
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

	// Rolling back to the first orphans the two after it.
	require.NoError(t, c.Rollback(ctx, checkpoints[0]))
	forceGC(t, c)

	info, err := c.container.Info(ctx)
	require.NoError(t, err)
	sn := c.client.SnapshotService(info.Snapshotter)
	for i, key := range checkpoints {
		_, err := sn.Stat(ctx, key)
		assert.NoError(t, err, "checkpoint %d must outlive the branch it was on", i+1)
	}

	// Which is the point: a later checkpoint is still reachable.
	require.NoError(t, c.Rollback(ctx, checkpoints[1]))
	res, err := c.Run(ctx, "cat /steps.txt", nil)
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\n", res.Stdout)
}

// forceGC runs a containerd garbage collection pass: deleting a lease
// synchronously sweeps everything left unreferenced.
func forceGC(t *testing.T, c *Container) {
	t.Helper()
	ctx := context.Background()
	l, err := c.client.LeasesService().Create(ctx, leases.WithRandomID())
	require.NoError(t, err)
	require.NoError(t, c.client.LeasesService().Delete(ctx, l, leases.SynchronousDelete))
}

// TestContainer_NetworkPosture: NetworkNone is a namespace with only
// loopback in it — no DNS, nothing fetchable. NetworkHost drops that
// namespace so the container inherits the containerd daemon's own,
// which is what makes a goal able to clone or install anything.
func TestContainer_NetworkPosture(t *testing.T) {
	for _, tc := range []struct {
		mode      string
		wantExtra bool // interfaces beyond loopback
		wantDNS   bool
	}{
		{NetworkNone, false, false},
		{NetworkHost, true, true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			if !daemonAvailable() {
				t.Skip("containerd not reachable at", testSocket)
			}
			c := NewContainer(WithSocket(testSocket), WithNamespace("detent-test"), WithNetwork(tc.mode))
			id := testContainerID(t)
			require.NoError(t, c.Start(context.Background(), id))
			t.Cleanup(func() { _ = c.Close(context.Background()) })

			ifaces, err := c.Run(context.Background(),
				"cat /proc/net/dev | tail -n +3 | awk '{print $1}' | tr -d ' :' | tr '\\n' ' '", nil)
			require.NoError(t, err)
			assert.Contains(t, ifaces.Stdout, "lo")
			assert.Equal(t, tc.wantExtra, strings.TrimSpace(ifaces.Stdout) != "lo",
				"interfaces were %q", strings.TrimSpace(ifaces.Stdout))

			dns, err := c.Run(context.Background(),
				"getent hosts github.com >/dev/null 2>&1 && echo yes || echo no", nil)
			require.NoError(t, err)
			assert.Equal(t, tc.wantDNS, strings.TrimSpace(dns.Stdout) == "yes", "DNS resolution")
		})
	}
}

// A heredoc is how a model writes a file, and its terminator has to be
// alone on its line. Wrapping the command as "( cmd ) >out 2>err"
// appended that tail to the terminator's line, so the shell read to
// end-of-input looking for one it would never find: exit 2, before
// running anything. Every multi-line command broke the same way.
func TestContainer_RunHandlesMultilineCommands(t *testing.T) {
	c := newTestContainer(t)
	ctx := context.Background()

	for _, tc := range []struct{ name, command string }{
		{"heredoc", "cat > /tmp/a.py <<'EOF'\nimport random\n\ndef f():\n    return 1\nEOF"},
		{"space before the quote", "cat > /tmp/a.py << 'EOF'\nimport random\n\ndef f():\n    return 1\nEOF"},
		{"trailing newline", "cat > /tmp/a.py <<'EOF'\nimport random\n\ndef f():\n    return 1\nEOF\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Run(ctx, "rm -f /tmp/a.py", nil)
			require.NoError(t, err)

			res, err := c.Run(ctx, tc.command, nil)
			require.NoError(t, err)
			require.Equal(t, 0, res.ExitCode, "stderr was %q", res.Stderr)

			back, err := c.Run(ctx, "cat /tmp/a.py", nil)
			require.NoError(t, err)
			assert.Equal(t, "import random\n\ndef f():\n    return 1\n", back.Stdout,
				"the file's blank lines and indentation must survive verbatim")
		})
	}

	// A plain multi-line script, and the exit code still belongs to the
	// command rather than the wrapper.
	res, err := c.Run(ctx, "x=1\ny=2\necho $((x + y))", nil)
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode, "stderr was %q", res.Stderr)
	assert.Equal(t, "3\n", res.Stdout)

	res, err = c.Run(ctx, "echo out\necho err >&2\nexit 7", nil)
	require.NoError(t, err)
	assert.Equal(t, 7, res.ExitCode)
	assert.Equal(t, "out\n", res.Stdout)
	assert.Equal(t, "err\n", res.Stderr)
}

// The engine runs read-only Calls together. Unserialised, they
// overwrote each other's spec and returned exit 0 with no output,
// which the model read as a command that printed nothing.
func TestContainer_ConcurrentRunsDoNotCrossContaminate(t *testing.T) {
	c := newTestContainer(t)

	const n = 6
	type got struct {
		want string
		res  capture.Result
		err  error
	}
	out := make([]got, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			want := fmt.Sprintf("marker-%d", i)
			res, err := c.Run(context.Background(), "echo "+want, nil)
			out[i] = got{want: want, res: res, err: err}
		}()
	}
	wg.Wait()

	for i, g := range out {
		require.NoError(t, g.err, "call %d failed", i)
		assert.Equal(t, 0, g.res.ExitCode, "call %d", i)
		assert.Equal(t, g.want, strings.TrimSpace(g.res.Stdout),
			"call %d got another command's output, or none", i)
	}
}
