package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each skill must be where the runner looks: the container sees the
// workspace under its mount point and nothing else of this machine.
func TestFindSkills_PathsAreWhereTheRunnerSeesThem(t *testing.T) {
	base := t.TempDir()
	repo, home := filepath.Join(base, "repo"), filepath.Join(base, "home")
	write(t, filepath.Join(repo, ".git/HEAD"), "ref")
	write(t, filepath.Join(repo, ".agents/skills/root-skill/SKILL.md"), "---\nname: root-skill\ndescription: d\n---\n")
	write(t, filepath.Join(repo, "sub/.claude/skills/near/SKILL.md"), "---\nname: near\ndescription: d\n---\n")
	write(t, filepath.Join(home, ".agents/skills/mine/SKILL.md"), "---\nname: mine\ndescription: d\ndisable-model-invocation: true\n---\n")
	cwd := filepath.Join(repo, "sub")

	host := findSkills(cwd, home, "/workspace", false)
	assert.Empty(t, host.mounts, "the host needs nothing mounted")
	assert.Equal(t, filepath.Join(home, ".agents/skills/mine"), dirOf(host, "mine"))

	box := findSkills(cwd, home, "/workspace", true)
	assert.Equal(t, "/workspace/.claude/skills/near", dirOf(box, "near"), "under cwd, so already mounted")
	assert.Equal(t, map[string]string{
		filepath.Join(repo, ".agents/skills"): "/opt/detent/skills/2",
		filepath.Join(home, ".agents/skills"): "/opt/detent/skills/4",
	}, box.mounts, "above cwd and in home, so each folder is mounted")
	assert.Equal(t, "/opt/detent/skills/2/root-skill", dirOf(box, "root-skill"))
	assert.Equal(t, "/opt/detent/skills/4/mine", dirOf(box, "mine"))

	for _, e := range box.entries {
		assert.Equal(t, e.Name == "mine", e.Hidden, "only the manual skill is kept from the catalog")
	}
	require.Len(t, box.skillTools(), 1)
	assert.Empty(t, findSkills(t.TempDir(), "", "/workspace", true).skillTools(), "no skills, no tool")
}

func dirOf(f foundSkills, name string) string {
	for _, e := range f.entries {
		if e.Name == name {
			return e.Dir
		}
	}
	return ""
}

func write(t *testing.T, p, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
}
