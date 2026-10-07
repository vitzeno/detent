package main

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/worktree"
)

func TestOpenWorktree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // where Windows looks for home
	repo := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "git", "init", "-q")
	cmd.Dir = repo
	require.NoError(t, cmd.Run())

	wt, err := openWorktree(true, repo)
	require.NoError(t, err)
	assert.NotNil(t, wt, "a git work tree is checkpointed")

	wt, err = openWorktree(true, t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, wt, "so is a plain directory, in a git directory of detent's own")
	require.NoError(t, wt.Close())
	assert.DirExists(t, worktree.PrivateStore())

	_, err = openWorktree(true, home)
	require.ErrorIs(t, err, worktree.ErrTooBroad, "the home directory is too broad, which is said")

	wt, err = openWorktree(false, repo)
	require.NoError(t, err)
	assert.Nil(t, wt, "headless has no undo to offer")
}
