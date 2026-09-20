package fileio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRead(t *testing.T) {
	t.Run("reads existing content", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "f.txt")
		require.NoError(t, os.WriteFile(path, []byte("hello\n"), 0o644))

		content, truncated, err := Read(path)
		require.NoError(t, err)
		assert.Equal(t, "hello\n", content)
		assert.False(t, truncated)
	})

	t.Run("truncates past MaxBytes and reports it", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "big.txt")
		big := strings.Repeat("x", MaxBytes+100)
		require.NoError(t, os.WriteFile(path, []byte(big), 0o644))

		content, truncated, err := Read(path)
		require.NoError(t, err)
		assert.True(t, truncated)
		assert.Len(t, content, MaxBytes)
	})

	t.Run("missing file is an error", func(t *testing.T) {
		_, _, err := Read(filepath.Join(t.TempDir(), "nope.txt"))
		assert.Error(t, err)
	})
}

func TestDiff(t *testing.T) {
	t.Run("identical content diffs to empty", func(t *testing.T) {
		assert.Empty(t, Diff("f.txt", "same\n", "same\n"))
	})

	t.Run("changed content produces a unified diff", func(t *testing.T) {
		d := Diff("f.txt", "line one\nline two\n", "line one\nline TWO\n")
		assert.Contains(t, d, "-line two")
		assert.Contains(t, d, "+line TWO")
		assert.Contains(t, d, "f.txt")
	})
}

func TestWrite(t *testing.T) {
	t.Run("writes new content and it round-trips", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "f.txt")
		require.NoError(t, Write(path, "content\n"))

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "content\n", string(got))
	})

	t.Run("overwriting preserves existing permissions", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "f.txt")
		require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o600))

		require.NoError(t, Write(path, "new\n"))

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "new\n", string(got))
	})
}
