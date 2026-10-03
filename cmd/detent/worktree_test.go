package main

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenWorktree(t *testing.T) {
	repo := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "git", "init", "-q")
	cmd.Dir = repo
	require.NoError(t, cmd.Run())

	wt, err := openWorktree(true, repo)
	require.NoError(t, err)
	assert.NotNil(t, wt, "a git work tree is checkpointed")

	wt, err = openWorktree(true, t.TempDir())
	require.NoError(t, err)
	assert.Nil(t, wt, "a plain directory is not, and that is no error")

	wt, err = openWorktree(false, repo)
	require.NoError(t, err)
	assert.Nil(t, wt, "headless has no undo to offer")
}
