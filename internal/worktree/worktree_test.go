package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repo is a real git repository, because this package is a thin shell
// over git plumbing and a fake would prove nothing about it.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		require.NoError(t, cmd.Run(), "git %v", args)
	}
	write(t, dir, "tracked.txt", "original\n")
	write(t, dir, ".gitignore", "ignored/\n")
	commit(t, dir)
	return dir
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	require.NoError(t, err)
	return string(b)
}

func commit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "x"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		require.NoError(t, cmd.Run(), "git %v", args)
	}
}

func TestAvailable(t *testing.T) {
	ctx := context.Background()
	assert.True(t, Available(ctx, repo(t)))
	assert.False(t, Available(ctx, t.TempDir()), "a plain directory can't be checkpointed")
}

// The three things a goal does to a file, each undone: a modified file
// comes back, a deleted one comes back, and one created since the
// checkpoint is removed.
func TestRestore_UndoesWhatAGoalDid(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)

	before, err := Capture(ctx, dir)
	require.NoError(t, err)

	write(t, dir, "tracked.txt", "edited by the model\n")
	write(t, dir, "created.py", "print(1)\n")
	require.NoError(t, os.Remove(filepath.Join(dir, ".gitignore")))

	changes, err := Diff(ctx, dir, before)
	require.NoError(t, err)
	got := map[string]Kind{}
	for _, c := range changes {
		got[c.Path] = c.Kind
	}
	assert.Equal(t, Restored, got["tracked.txt"], "an edit is reverted")
	assert.Equal(t, Restored, got[".gitignore"], "a deletion is undone")
	assert.Equal(t, Removed, got["created.py"], "a file created since is deleted")

	require.NoError(t, Restore(ctx, dir, before))
	assert.Equal(t, "original\n", read(t, dir, "tracked.txt"))
	assert.FileExists(t, filepath.Join(dir, ".gitignore"))
	assert.NoFileExists(t, filepath.Join(dir, "created.py"))

	after, err := Diff(ctx, dir, before)
	require.NoError(t, err)
	assert.Empty(t, after, "nothing left to do once restored")
}

// Untracked files are the normal case for a goal that writes something
// new, so a checkpoint that only covered tracked files would miss most
// of what it is for.
func TestCapture_IncludesUntrackedFiles(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	write(t, dir, "notes.md", "# notes\n")

	before, err := Capture(ctx, dir)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, "notes.md")))

	require.NoError(t, Restore(ctx, dir, before))
	assert.Equal(t, "# notes\n", read(t, dir, "notes.md"), "an untracked file is restorable")
}

// Build output is not state worth reverting, and walking it would make
// every step slower. What git ignores, this ignores.
func TestCapture_SkipsIgnoredPaths(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	write(t, dir, "ignored/artifact.bin", "built\n")

	before, err := Capture(ctx, dir)
	require.NoError(t, err)
	write(t, dir, "ignored/artifact.bin", "rebuilt\n")

	changes, err := Diff(ctx, dir, before)
	require.NoError(t, err)
	assert.Empty(t, changes, "an ignored file is neither captured nor reverted")

	require.NoError(t, Restore(ctx, dir, before))
	assert.Equal(t, "rebuilt\n", read(t, dir, "ignored/artifact.bin"), "and is left alone")
}

// The whole point of the plumbing: checkpointing must be invisible to
// whatever the human has staged.
func TestCapture_LeavesTheUsersIndexAlone(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	write(t, dir, "staged.txt", "mine\n")

	add := exec.Command("git", "add", "staged.txt")
	add.Dir = dir
	require.NoError(t, add.Run())

	staged := func() string {
		cmd := exec.Command("git", "diff", "--cached", "--name-only")
		cmd.Dir = dir
		out, err := cmd.Output()
		require.NoError(t, err)
		return string(out)
	}
	was := staged()
	require.Contains(t, was, "staged.txt")

	_, err := Capture(ctx, dir)
	require.NoError(t, err)
	assert.Equal(t, was, staged(), "the user's index must be untouched")
}

// Nothing changed means nothing to confirm and nothing to write.
func TestDiff_EmptyWhenUnchanged(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	c, err := Capture(ctx, dir)
	require.NoError(t, err)

	changes, err := Diff(ctx, dir, c)
	require.NoError(t, err)
	assert.Empty(t, changes)
}
