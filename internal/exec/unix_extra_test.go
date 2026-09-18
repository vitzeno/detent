package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPortListeners(t *testing.T) {
	// No fixture control over what's actually listening on this machine —
	// just prove it runs a real lsof and doesn't error, the same
	// "at least doesn't crash on real system state" bar processList's own
	// test uses.
	result, err := portListeners(context.Background(), Args{})
	require.NoError(t, err)
	assert.NotNil(t, result) // Output may legitimately be empty (nothing listening)
}

func TestDiskUsage(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte(strings.Repeat("x", 1000)), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("y"), 0o644))

	tests := []struct {
		name    string
		args    Args
		wantErr bool
	}{
		{name: "real directory", args: Args{"path": dir}},
		{name: "nonexistent path errors", args: Args{"path": filepath.Join(dir, "nope")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := diskUsage(context.Background(), tt.args)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, result.Output, "sub")
		})
	}
}

func TestDiskUsage_DefaultsToCWD(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "only.txt"), []byte("x"), 0o644))
	t.Chdir(dir)

	result, err := diskUsage(context.Background(), Args{})
	require.NoError(t, err)
	assert.NotEmpty(t, result.Output)
}

func TestFindFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "top.txt"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nested", "deep"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nested", "deep", "buried.txt"), []byte("x"), 0o644))

	result, err := findFiles(context.Background(), Args{"path": dir})
	require.NoError(t, err)
	assert.Contains(t, result.Output, "top.txt")
	assert.Contains(t, result.Output, "buried.txt")
}

func TestTailLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	require.NoError(t, os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644))

	tests := []struct {
		name    string
		args    Args
		wantErr bool
	}{
		{name: "reads a real log file", args: Args{"path": path}},
		{name: "missing path arg", args: Args{}, wantErr: true},
		{name: "nonexistent file", args: Args{"path": filepath.Join(dir, "nope.log")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tailLog(context.Background(), tt.args)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, result.Output, "line3")
		})
	}
}

func TestGitStatus_RealRepo(t *testing.T) {
	// Runs against this actual repo (no path arg) — read-only, safe.
	result, err := gitStatus(context.Background(), Args{})
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestGitLog_RealRepo(t *testing.T) {
	result, err := gitLog(context.Background(), Args{})
	require.NoError(t, err)
	assert.NotEmpty(t, result.Output, "this repo has real commits")
}

func TestRegisterUnix_WiresExtraHandlers(t *testing.T) {
	r := NewRegistry()
	RegisterUnix(r)
	for _, name := range []string{
		"unix__port_listeners", "unix__disk_usage", "unix__find_files",
		"unix__tail_log", "unix__git_status", "unix__git_log",
	} {
		assert.True(t, r.Names()[name], "expected %s to be registered", name)
	}
}
