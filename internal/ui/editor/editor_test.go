package editor

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.txt")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestNew_LoadsContent(t *testing.T) {
	path := writeTemp(t, "hello\n")
	m := New(path)
	require.NoError(t, m.Err())
	assert.Equal(t, "hello\n", m.Value())
	assert.False(t, m.Dirty())
	assert.False(t, m.Truncated())
}

func TestNew_MissingFileKeepsErrorInsteadOfPanicking(t *testing.T) {
	m := New(filepath.Join(t.TempDir(), "nope.txt"))
	assert.Error(t, m.Err())
	assert.Contains(t, m.View(), "could not open")
}

func TestDirtyAndDiff(t *testing.T) {
	path := writeTemp(t, "old\n")
	m := New(path)
	assert.False(t, m.Dirty())
	assert.Empty(t, m.Diff())

	m.Focus()
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	assert.True(t, nm.Dirty())
	assert.Contains(t, nm.Diff(), "+X")
}

func TestMarkSaved_ClearsDirty(t *testing.T) {
	path := writeTemp(t, "old\n")
	m := New(path)
	m.Focus()
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	require.True(t, nm.Dirty())

	nm.MarkSaved(nm.Value())
	assert.False(t, nm.Dirty())
	assert.Empty(t, nm.Diff())
}

func TestResize_SkipsRedundantCalls(t *testing.T) {
	path := writeTemp(t, "hi\n")
	m := New(path)
	m.Resize(40, 10)
	m.Resize(40, 10) // same size again: must not disturb scroll state
	assert.Equal(t, 40, m.lastW)
	assert.Equal(t, 10, m.lastH)
}

func TestFocusBlur(t *testing.T) {
	path := writeTemp(t, "hi\n")
	m := New(path)
	cmd := m.Focus()
	assert.NotNil(t, cmd, "Focus returns a blink cmd")
	m.Blur()
}
