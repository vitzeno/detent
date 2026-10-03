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

	containerd "github.com/containerd/containerd"
	"github.com/containerd/containerd/leases"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capture"
)

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

// A skill is mounted for reading: the model can load it, never rewrite it.
func TestContainer_ReadOnlyMountsCannotBeWritten(t *testing.T) {
	wd, err := os.Getwd()
	require.NoError(t, err)
	// Under the working directory, since colima shares only $HOME with its VM.
	src := filepath.Join(wd, ".sandbox-test-skills")
	require.NoError(t, os.MkdirAll(src, 0o755))
	t.Cleanup(func() { os.RemoveAll(src) })
	require.NoError(t, os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("be careful\n"), 0o644))

	c := newTestContainer(t, WithReadOnly(map[string]string{src: "/opt/detent/skills/0"}))
	ctx := context.Background()

	res, err := c.Run(ctx, "cat /opt/detent/skills/0/SKILL.md", nil)
	require.NoError(t, err)
	assert.Equal(t, "be careful\n", res.Stdout)

	res, err = c.Run(ctx, "echo changed > /opt/detent/skills/0/SKILL.md", nil)
	require.NoError(t, err)
	assert.NotEqual(t, 0, res.ExitCode, "the write must fail")
	got, _ := os.ReadFile(filepath.Join(src, "SKILL.md"))
	assert.Equal(t, "be careful\n", string(got))
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

// TestContainer_RollbackLeavesTheWorkspaceAlone pins a real limit: the
// workspace is a bind mount, not snapshotted, so a rollback keeps its edits.
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

// TestContainer_RollbackTargetsTheRightCheckpoint snapshots after every
// step, so an off-by-one in either direction fails here.
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

	// Checkpoint 2 is still reachable after rolling back past it.
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

// TestContainer_NetworkPosture: NetworkNone has only loopback and no DNS,
// NetworkHost inherits the daemon's own network namespace.
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

// A heredoc's terminator must stay alone on its line, so the output
// redirection must not be appended to the command.
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

	// The exit code belongs to the command, not the wrapper.
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

// The engine runs read-only Calls together, and each must get its own output.
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

// A killed process never runs Close, so resuming its session must start
// over the container, snapshot and lease it left.
func TestContainer_StartsOverWhatAKilledProcessLeftBehind(t *testing.T) {
	if !daemonAvailable() {
		t.Skip("containerd not reachable at", testSocket)
	}
	ctx := context.Background()
	id := testContainerID(t)

	killed := NewContainer(WithSocket(testSocket), WithNamespace("detent-test"))
	require.NoError(t, killed.Start(ctx, id))
	_, err := killed.Run(ctx, "echo first > /tmp/marker", nil)
	require.NoError(t, err)
	// No Close: that is the whole point.

	resumed := NewContainer(WithSocket(testSocket), WithNamespace("detent-test"))
	require.NoError(t, resumed.Start(ctx, id), "the session could not start over its own leftovers")
	t.Cleanup(func() { _ = resumed.Close(ctx) })

	res, err := resumed.Run(ctx, "echo ok", nil)
	require.NoError(t, err)
	assert.Equal(t, "ok\n", res.Stdout)
}

// Clearing must not reach into a session another detent is running.
func TestContainer_RefusesASessionThatIsStillRunning(t *testing.T) {
	if !daemonAvailable() {
		t.Skip("containerd not reachable at", testSocket)
	}
	ctx := context.Background()
	id := testContainerID(t)

	live := NewContainer(WithSocket(testSocket), WithNamespace("detent-test"))
	require.NoError(t, live.Start(ctx, id))
	t.Cleanup(func() { _ = live.Close(ctx) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = live.Run(ctx, "sleep 5", nil)
	}()
	t.Cleanup(func() { <-done })

	// Waited for, not polled with Start, which would clear it if it won the race.
	require.Eventually(t, func() bool { return taskRunning(t, id) },
		5*time.Second, 100*time.Millisecond, "the live task never started")

	other := NewContainer(WithSocket(testSocket), WithNamespace("detent-test"))
	err := other.Start(ctx, id)
	require.ErrorContains(t, err, "already running",
		"a live session was cleared out from under another process")
}

// A cancelled command must not outlive its Run, or every later Run fails to create a task.
func TestContainer_RunsAgainAfterACancel(t *testing.T) {
	c := newTestContainer(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := c.Run(ctx, "sleep 30", nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	res, err := c.Run(context.Background(), "echo ok", nil)
	require.NoError(t, err, "the cancelled task was left behind")
	assert.Equal(t, "ok\n", res.Stdout)
}

// Between Calls a live session has no task, so its holder is what keeps it.
func TestPrune_LeavesAnIdleLiveSessionAlone(t *testing.T) {
	if !daemonAvailable() {
		t.Skip("containerd not reachable at", testSocket)
	}
	ctx := context.Background()
	const ns = "detent-test-prune-idle"
	id := testContainerID(t)

	live := NewContainer(WithSocket(testSocket), WithNamespace(ns))
	require.NoError(t, live.Start(ctx, id))
	t.Cleanup(func() { _ = live.Close(ctx) })
	// Held by another process on this machine: the test's parent will do.
	host, err := os.Hostname()
	require.NoError(t, err)
	_, err = live.container.SetLabels(ctx, map[string]string{holderLabel: fmt.Sprintf("%d@%s", os.Getppid(), host)})
	require.NoError(t, err)

	out, err := Prune(ctx, testSocket, ns)
	require.NoError(t, err)
	assert.Contains(t, out.Kept, id)

	res, err := live.Run(ctx, "echo survived", nil)
	require.NoError(t, err, "prune broke an idle live session")
	assert.Equal(t, "survived\n", res.Stdout)
}

// A session nobody resumes leaks its container forever, since only
// Close and clearStale delete one and neither will ever run again.
func TestPrune_RemovesWhatAnAbandonedSessionLeft(t *testing.T) {
	if !daemonAvailable() {
		t.Skip("containerd not reachable at", testSocket)
	}
	ctx := context.Background()
	const ns = "detent-test-prune"
	id := testContainerID(t)

	abandoned := NewContainer(WithSocket(testSocket), WithNamespace(ns))
	require.NoError(t, abandoned.Start(ctx, id))
	// No Close: this is a session nobody comes back to.

	out, err := Prune(ctx, testSocket, ns)
	require.NoError(t, err)
	assert.Contains(t, out.Containers, id)
	assert.Empty(t, out.Kept)

	again, err := Prune(ctx, testSocket, ns)
	require.NoError(t, err)
	assert.True(t, again.Empty(), "a second prune found something to do")
}

// Pruning must not reach into a session that is still working.
func TestPrune_LeavesALiveSessionAlone(t *testing.T) {
	if !daemonAvailable() {
		t.Skip("containerd not reachable at", testSocket)
	}
	ctx := context.Background()
	const ns = "detent-test-prune-live"
	id := testContainerID(t)

	live := NewContainer(WithSocket(testSocket), WithNamespace(ns))
	require.NoError(t, live.Start(ctx, id))
	t.Cleanup(func() { _ = live.Close(ctx) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = live.Run(ctx, "sleep 5", nil)
	}()
	t.Cleanup(func() { <-done })

	require.Eventually(t, func() bool { return taskRunningIn(t, ns, id) },
		5*time.Second, 100*time.Millisecond, "the live task never started")

	out, err := Prune(ctx, testSocket, ns)
	require.NoError(t, err)
	assert.Contains(t, out.Kept, id)
	assert.NotContains(t, out.Containers, id)

	res, err := live.Run(ctx, "echo survived", nil)
	require.NoError(t, err, "prune broke a live session")
	assert.Equal(t, "survived\n", res.Stdout)
}

// A deleted session must not leave a container nothing will resume.
func TestForget_RemovesOneSessionsContainer(t *testing.T) {
	if !daemonAvailable() {
		t.Skip("containerd not reachable at", testSocket)
	}
	ctx := context.Background()
	const ns = "detent-test-forget"
	gone, kept := testContainerID(t), testContainerID(t)

	for _, id := range []string{gone, kept} {
		c := NewContainer(WithSocket(testSocket), WithNamespace(ns))
		require.NoError(t, c.Start(ctx, id))
	}
	t.Cleanup(func() { _, _ = Prune(ctx, testSocket, ns) })

	require.NoError(t, Forget(ctx, testSocket, ns, gone))

	out, err := Prune(ctx, testSocket, ns)
	require.NoError(t, err)
	assert.NotContains(t, out.Containers, gone, "Forget left it for Prune")
	assert.Contains(t, out.Containers, kept, "Forget took the wrong one")
}

// Forgetting a session that never had a container is not a failure.
func TestForget_AbsentIsNotAnError(t *testing.T) {
	if !daemonAvailable() {
		t.Skip("containerd not reachable at", testSocket)
	}
	assert.NoError(t, Forget(context.Background(), testSocket, "detent-test-forget", testContainerID(t)))
}

// testSocket is colima's default profile. containerd.New takes a bare
// path, not a unix:// URI.
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

// testContainerID is unique per run: containerd reclaims a dropped
// snapshot asynchronously, so a reused name can inherit the last run's files.
func testContainerID(t *testing.T) string {
	t.Helper()
	var b [6]byte
	_, err := rand.Read(b[:])
	require.NoError(t, err)
	return strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-") + "-" + hex.EncodeToString(b[:])
}

func newTestContainer(t *testing.T, opts ...Option) *Container {
	t.Helper()
	if !daemonAvailable() {
		t.Skip("containerd not reachable at", testSocket)
	}
	c := NewContainer(append([]Option{WithSocket(testSocket), WithNamespace("detent-test")}, opts...)...)
	id := testContainerID(t)
	require.NoError(t, c.Start(context.Background(), id))
	t.Cleanup(func() {
		_ = c.Close(context.Background())
	})
	return c
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

// taskRunning reports whether the session's container has a running
// task, read-only, so waiting for one cannot disturb it.
func taskRunning(t *testing.T, sessionID string) bool {
	t.Helper()
	return taskRunningIn(t, "detent-test", sessionID)
}

func taskRunningIn(t *testing.T, namespace, sessionID string) bool {
	t.Helper()
	client, err := containerd.New(testSocket, containerd.WithDefaultNamespace(namespace))
	if err != nil {
		return false
	}
	defer client.Close()
	ctx := context.Background()
	cont, err := client.LoadContainer(ctx, containerID(sessionID))
	if err != nil {
		return false
	}
	task, err := cont.Task(ctx, nil)
	if err != nil {
		return false
	}
	st, err := task.Status(ctx)
	return err == nil && st.Status == containerd.Running
}
