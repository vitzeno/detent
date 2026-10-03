//go:build !windows

package tool

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A FIFO or a device is refused before it is opened, since opening or
// reading one can block for ever.
func TestNative_RefusesWhatCouldBlock(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, syscall.Mkfifo("pipe", 0o600))
	require.NoError(t, os.Mkdir("skill", 0o755))
	require.NoError(t, syscall.Mkfifo("skill/SKILL.md", 0o600))
	for _, c := range []struct {
		tl   Native
		args Args
		why  string
	}{
		{ReadFile{}, Args{"path": "pipe"}, "pipe: is a named pipe, not a regular file"},
		{ReadFile{}, Args{"path": "/dev/zero"}, "/dev/zero: is a device, not a regular file"},
		{Grep{}, Args{"pattern": "x", "path": "pipe"}, "pipe: is a named pipe"},
		{Grep{}, Args{"pattern": "x", "path": "/dev/zero"}, "/dev/zero: is a device"},
		{EditFile{}, Args{"path": "pipe", "old_string": "a", "new_string": "b"}, "pipe: is a named pipe"},
		{WriteFile{}, Args{"path": "pipe", "content": "x"}, "pipe: is a named pipe"},
		{Skill{Entries: []SkillEntry{{Name: "s", Dir: "skill"}}}, Args{"name": "s"}, "is a named pipe"},
	} {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		got := c.tl.Run(ctx, c.args)
		err := ctx.Err()
		cancel()
		require.NoError(t, err, "%s returned before its deadline", c.tl.Name())
		assert.NotZero(t, got.ExitCode, c.tl.Name())
		assert.Contains(t, got.Stderr, c.why, c.tl.Name())
	}
}
