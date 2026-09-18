package exec

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))

	tests := []struct {
		name      string
		args      Args
		wantLines []string // exact, order matters — sorted output
		wantErr   bool
	}{
		{
			name:      "lists files and directories, sorted, dirs suffixed",
			args:      Args{"path": dir},
			wantLines: []string{"a.txt", "b.txt", "sub/"},
		},
		{
			name:    "nonexistent path errors",
			args:    Args{"path": filepath.Join(dir, "does-not-exist")},
			wantErr: true,
		},
		{
			name:    "wrong arg type errors",
			args:    Args{"path": 42},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := listFiles(context.Background(), tt.args)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantLines, strings.Split(result.Output, "\n"))
		})
	}
}

func TestListFiles_DefaultsToCWD(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "only.txt"), []byte("x"), 0o644))
	t.Chdir(dir)

	result, err := listFiles(context.Background(), Args{})
	require.NoError(t, err)
	assert.Equal(t, "only.txt", result.Output)
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "hello.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("hello, detent\n"), 0o644))

	tests := []struct {
		name       string
		args       Args
		wantOutput string
		wantErr    bool
	}{
		{name: "reads file contents", args: Args{"path": filePath}, wantOutput: "hello, detent\n"},
		{name: "missing path arg", args: Args{}, wantErr: true},
		{name: "wrong arg type", args: Args{"path": 42}, wantErr: true},
		{name: "nonexistent file", args: Args{"path": filepath.Join(dir, "nope.txt")}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := readFile(context.Background(), tt.args)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantOutput, result.Output)
		})
	}
}

func TestProcessList(t *testing.T) {
	result, err := processList(context.Background(), Args{})
	require.NoError(t, err)
	assert.NotEmpty(t, result.Output)

	// Sanity check against real data: this test binary is itself a running
	// process, so its own pid must appear somewhere in real `ps` output.
	assert.Contains(t, result.Output, strconv.Itoa(os.Getpid()))
}

// spawnSleeper starts a real, disposable child process — safe to signal
// because it belongs to this test, not anything the user cares about —
// and returns its pid plus a cleanup that reaps it if the test itself
// doesn't kill it first.
func spawnSleeper(t *testing.T) int {
	t.Helper()
	cmd := osexec.Command("sleep", "30")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd.Process.Pid
}

func TestKillProcess(t *testing.T) {
	t.Run("sends a real signal to a real disposable process", func(t *testing.T) {
		pid := spawnSleeper(t)

		result, err := killProcess(context.Background(), Args{"pid": pid})
		require.NoError(t, err)
		assert.Contains(t, result.Output, strconv.Itoa(pid))

		proc, _ := os.FindProcess(pid)
		done := make(chan error, 1)
		go func() { _, err := proc.Wait(); done <- err }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("process did not exit after SIGTERM")
		}
	})

	tests := []struct {
		name string
		args Args
	}{
		{name: "missing pid arg", args: Args{}},
		{name: "wrong arg type", args: Args{"pid": "4821"}},
		{name: "nonexistent pid", args: Args{"pid": 999999}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := killProcess(context.Background(), tt.args)
			assert.Error(t, err)
		})
	}
}

func TestRegisterUnix_WiresAllHandlers(t *testing.T) {
	r := NewRegistry()
	RegisterUnix(r)

	assert.Equal(t, map[string]bool{
		"unix__list_files":     true,
		"unix__read_file":      true,
		"unix__process_list":   true,
		"unix__kill_process":   true,
		"unix__port_listeners": true,
		"unix__disk_usage":     true,
		"unix__find_files":     true,
		"unix__tail_log":       true,
		"unix__git_status":     true,
		"unix__git_log":        true,
	}, r.Names())
}
