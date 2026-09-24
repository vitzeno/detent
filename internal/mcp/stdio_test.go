package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServer builds testdata/fakeserver once and returns its path.
// Built rather than skipped: stdio is the transport every configured
// server arrives on, so CI has to actually exercise it.
var fakeServer = sync.OnceValues(func() (string, error) {
	bin := filepath.Join(buildDir, "fakeserver")
	out, err := exec.Command("go", "build", "-o", bin, "./testdata/fakeserver").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("building testdata/fakeserver: %v\n%s", err, out)
	}
	return bin, nil
})

var buildDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "detent-mcp")
	if err != nil {
		panic(err)
	}
	buildDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
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

// The in-memory tests never launch anything. This one does: a real
// subprocess over real pipes, which is how a configured server arrives.
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

// A server that outlives its session is what the next run trips over,
// which is the lesson internal/sandbox learned the expensive way.
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

// running counts live processes started from bin.
func running(t *testing.T, bin string) int {
	t.Helper()
	out, err := exec.Command("pgrep", "-f", bin).Output()
	if err != nil {
		return 0 // pgrep exits non-zero when nothing matches
	}
	return len(strings.Fields(string(out)))
}

// A command that is not there must not panic or hang.
func TestStdio_AMissingCommandIsAnError(t *testing.T) {
	_, err := Connect(context.Background(), "nope",
		Stdio{Command: "/nonexistent/detent-mcp-server"}.Transport())
	assert.Error(t, err)
}
