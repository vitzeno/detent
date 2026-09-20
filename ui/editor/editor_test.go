package editor

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_WrapsContent(t *testing.T) {
	m := New("f.txt", "hello\n", false, 0, nil)
	require.NoError(t, m.Err())
	assert.Equal(t, "hello\n", m.Value())
	assert.False(t, m.Dirty())
	assert.False(t, m.Truncated())
}

func TestNew_LoadErrorKeptInsteadOfPanicking(t *testing.T) {
	m := New("nope.txt", "", false, 0, fmt.Errorf("open nope.txt: no such file or directory"))
	assert.Error(t, m.Err())
	assert.Contains(t, m.View(), "could not open")
}

func TestDirtyAndDiff(t *testing.T) {
	m := New("f.txt", "old\n", false, 0, nil)
	assert.False(t, m.Dirty())
	assert.Empty(t, m.Diff())

	m.Focus()
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'X', Text: "X"})
	assert.True(t, nm.Dirty())
	assert.Contains(t, nm.Diff(), "+X")
}

func TestMarkSaved_ClearsDirty(t *testing.T) {
	m := New("f.txt", "old\n", false, 0, nil)
	m.Focus()
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'X', Text: "X"})
	require.True(t, nm.Dirty())

	nm.MarkSaved(nm.Value())
	assert.False(t, nm.Dirty())
	assert.Empty(t, nm.Diff())
}

func TestResize_SkipsRedundantCalls(t *testing.T) {
	m := New("f.txt", "hi\n", false, 0, nil)
	m.Resize(40, 10)
	m.Resize(40, 10) // same size again: must not disturb scroll state
	assert.Equal(t, 40, m.lastW)
	assert.Equal(t, 10, m.lastH)
}

func TestFocusBlur(t *testing.T) {
	m := New("f.txt", "hi\n", false, 0, nil)
	cmd := m.Focus()
	assert.NotNil(t, cmd, "Focus returns a blink cmd")
	m.Blur()
}
