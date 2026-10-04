package skills

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shipped skills are written as files a runner can read, and load as skills.
func TestBuiltin_WritesSkillsThatLoad(t *testing.T) {
	dir := t.TempDir()
	root, err := Builtin(dir)
	require.NoError(t, err)
	assert.True(t, root.Builtin)

	found, warnings := Find([]Root{root})
	assert.Empty(t, warnings, "a shipped skill must load cleanly")
	require.NotEmpty(t, found)
	var creator *Skill
	for i := range found {
		if found[i].Name == "skill-creator" {
			creator = &found[i]
		}
	}
	require.NotNil(t, creator)
	assert.True(t, creator.Builtin)
	assert.True(t, creator.ModelInvocable)
	assert.True(t, creator.UserInvocable)
	assert.FileExists(t, filepath.Join(dir, "skill-creator", "SKILL.md"))
}

// Startup writes nothing when this build ships what is already there.
func TestBuiltin_LeavesAnUnchangedSkillAlone(t *testing.T) {
	dir := t.TempDir()
	_, err := Builtin(dir)
	require.NoError(t, err)
	file := filepath.Join(dir, "skill-creator", "SKILL.md")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	require.NoError(t, os.Chtimes(file, old, old))

	_, err = Builtin(dir)
	require.NoError(t, err)
	info, err := os.Stat(file)
	require.NoError(t, err)
	assert.Equal(t, old, info.ModTime(), "an identical file is not rewritten")

	require.NoError(t, os.WriteFile(file, []byte("stale"), 0o644))
	_, err = Builtin(dir)
	require.NoError(t, err)
	got, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.NotEqual(t, "stale", string(got), "a changed one is brought back to what ships")
}

// Searched last, so a skill of the human's with the same name wins.
func TestBuiltin_YieldsToTheHumansOwn(t *testing.T) {
	mine := t.TempDir()
	write := filepath.Join(mine, "skill-creator", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(write), 0o755))
	require.NoError(t, os.WriteFile(write, []byte("---\nname: skill-creator\ndescription: mine\n---\n"), 0o644))
	builtin, err := Builtin(t.TempDir())
	require.NoError(t, err)

	found, _ := Find([]Root{{Dir: mine}, builtin})
	require.Len(t, found, 1)
	assert.Equal(t, "mine", found[0].Description)
	assert.False(t, found[0].Builtin)
}
