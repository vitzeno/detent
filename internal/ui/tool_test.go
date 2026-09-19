package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUI_TreeTool_OpensAndFocusesOutput(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi\n"), 0o644))
	t.Chdir(dir)

	m := testUIModel()
	nm, cmd := m.runSlash("/tree")
	m = nm.(Model)
	assert.Nil(t, cmd)
	assert.Equal(t, focusOutput, m.focus)

	require.Len(t, m.blocks, 1)
	b := m.blocks[0]
	assert.Equal(t, "tree", b.tool)
	assert.True(t, b.ended)
	require.Len(t, b.steps, 1)
	row := b.steps[0]
	assert.Equal(t, "tree", row.toolKind)
	require.NotNil(t, row.tree)

	v := m.View()
	assert.Contains(t, v, "f.txt")
}

func TestUI_TreeTool_NavigateAndOpenFileIntoEditor(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644))
	t.Chdir(dir)

	m := testUIModel()
	nm, _ := m.runSlash("/tree")
	m = nm.(Model)

	// Root is row 0 (already selected); down moves to a.txt.
	nm, _ = m.outputKey(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model)
	require.Len(t, m.blocks, 1, "navigating the tree must not itself open anything")

	treeRow := m.blocks[0].steps[0]
	require.Equal(t, "a.txt", treeRow.tree.Selected().Name)

	nm, _ = m.outputKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.Len(t, m.blocks, 2, "opening a file appends its own new block")

	fileBlock := m.blocks[1]
	assert.Equal(t, "file", fileBlock.tool)
	require.Len(t, fileBlock.steps, 1)
	fileRow := fileBlock.steps[0]
	require.NotNil(t, fileRow.editor)
	require.NoError(t, fileRow.editor.Err())
	assert.Equal(t, "hello\n", fileRow.editor.Value())

	// The explicit "open" action jumps straight to it.
	assert.Same(t, fileRow, m.focused())
}

func TestUI_TreeTool_ToggleDirDoesNotOpenAnything(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("b\n"), 0o644))
	t.Chdir(dir)

	m := testUIModel()
	nm, _ := m.runSlash("/tree")
	m = nm.(Model)

	nm, _ = m.outputKey(tea.KeyMsg{Type: tea.KeyDown}) // -> sub
	m = nm.(Model)
	treeRow := m.blocks[0].steps[0]
	require.Equal(t, "sub", treeRow.tree.Selected().Name)

	nm, _ = m.outputKey(tea.KeyMsg{Type: tea.KeyEnter}) // toggle expand
	m = nm.(Model)
	require.Len(t, m.blocks, 1, "expanding a directory must not open a new block")
	assert.Contains(t, m.View(), "b.txt")
}

func TestUI_HelpTool_ListsSlashCommands(t *testing.T) {
	m := testUIModel()
	nm, _ := m.runSlash("/help")
	m = nm.(Model)

	v := m.View()
	assert.Contains(t, v, "/quit")
	assert.Contains(t, v, "/tree")
	assert.Contains(t, v, "/usage")
}
