package mcp

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/tool"
)

// The in-memory tests launch nothing. This one uses real pipes.
func TestStdio_TalksToARealSubprocess(t *testing.T) {
	ctx := context.Background()
	s := stdioServer(t, "DETENT_MARKER=carried")

	tools, err := s.Tools(ctx)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "echo", tools[0].Name)

	res := s.Call(ctx, "echo", nil)
	assert.Zero(t, res.ExitCode)
	assert.Contains(t, res.Stdout, "from a real subprocess")
	assert.Contains(t, res.Stdout, "env=carried", "Env did not reach the server")
}

// A server that outlives its session is what the next run trips over.
func TestStdio_CloseEndsTheProcess(t *testing.T) {
	bin, err := fakeServer()
	require.NoError(t, err)
	before := running(t, bin)

	s, err := Connect(context.Background(), "subproc", Stdio{Command: bin}.Transport())
	require.NoError(t, err)
	_, err = s.Tools(context.Background())
	require.NoError(t, err)
	require.Greater(t, running(t, bin), before, "the test needs the server to have started")

	require.NoError(t, s.Close())
	assert.Eventually(t, func() bool { return running(t, bin) <= before },
		5*time.Second, 100*time.Millisecond, "the server outlived its session")
}

// A command that is not there must not panic or hang.
func TestStdio_AMissingCommandIsAnError(t *testing.T) {
	_, err := Connect(context.Background(), "nope",
		Stdio{Command: "/nonexistent/detent-mcp-server"}.Transport())
	assert.Error(t, err)
}

// An Env left nil is no environment, not all of detent's.
func TestStdio_ANilEnvInheritsNothing(t *testing.T) {
	bin, err := fakeServer()
	require.NoError(t, err)
	t.Setenv("DETENT_MARKER", "leaked")

	s, err := Connect(context.Background(), "subproc", Stdio{Command: bin}.Transport())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	res := s.Call(context.Background(), "echo", nil)
	assert.Contains(t, res.Stdout, "env=")
	assert.NotContains(t, res.Stdout, "leaked")
}

// A server that exits at once says why on stderr, and that is the one
// thing worth showing on /mcp.
func TestStdio_AFailedStartSaysWhatTheServerSaid(t *testing.T) {
	bin, err := fakeServer()
	require.NoError(t, err)
	_, err = Connect(context.Background(), "subproc",
		Stdio{Command: bin, Env: []string{"FAKESERVER_MODE=fail"}}.Transport())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAKE_TOKEN is not set")
}

// A server offering nothing is still a process, and Close must reach it.
func TestConnectAll_AServerWithNoToolsIsStillClosed(t *testing.T) {
	bin, err := fakeServer()
	require.NoError(t, err)
	before := running(t, bin)

	in := NewInvokers()
	errs := ConnectAll(context.Background(), tool.Standard(), in, map[string]Config{
		"empty": {Command: bin, Env: map[string]string{"FAKESERVER_MODE": "none"}},
	}, nil)
	require.Empty(t, errs)
	require.Len(t, in.Servers(), 1)
	require.Greater(t, running(t, bin), before)

	require.NoError(t, in.Close())
	assert.Eventually(t, func() bool { return running(t, bin) <= before },
		5*time.Second, 100*time.Millisecond, "a server with no tools outlived Close")
}

func TestTail_KeepsTheEnd(t *testing.T) {
	tl := &tail{max: 8}
	_, _ = tl.Write([]byte("0123456789"))
	_, _ = tl.Write([]byte("ab"))
	assert.Equal(t, "456789ab", tl.String())
}

func stdioServer(t *testing.T, env ...string) *Server {
	t.Helper()
	bin, err := fakeServer()
	require.NoError(t, err)

	s, err := Connect(context.Background(), "subproc", Stdio{Command: bin, Env: env}.Transport())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// fakeServer builds testdata/fakeserver once. Built rather than
// skipped, so CI exercises the transport every server arrives on.
var fakeServer = sync.OnceValues(func() (string, error) {
	bin := filepath.Join(buildDir, "fakeserver")
	out, err := exec.Command("go", "build", "-o", bin, "./testdata/fakeserver").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("building testdata/fakeserver: %w\n%s", err, out)
	}
	return bin, nil
})

var buildDir string

// running counts live processes started from bin.
func running(t *testing.T, bin string) int {
	t.Helper()
	out, err := exec.Command("pgrep", "-f", bin).Output()
	if err != nil {
		return 0 // pgrep exits non-zero when nothing matches
	}
	return len(strings.Fields(string(out)))
}
