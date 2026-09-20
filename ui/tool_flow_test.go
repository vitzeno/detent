package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for tool_flow.go: slash-command dispatch (/quit, /abort, /tree,
// /usage, /help) and the tool blocks they open.

func TestUI_SlashCommands(t *testing.T) {
	m := New(context.Background(), newFakeDriver(), SessionInfo{Proposer: "test-model"})
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()

	m.prompt.SetValue("/quit")
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	require.NotNil(t, cmd, "/quit must return the quit command")
	require.Empty(t, m.blocks, "/quit must not open a goal")

	m.prompt.SetValue("/bogus")
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	require.Empty(t, m.blocks)
	require.Contains(t, m.notice.text, "unknown command")

	m.prompt.SetValue("/abort")
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	require.Equal(t, "nothing running", m.notice.text)
}

func TestUI_AbortSlashWhileBusy(t *testing.T) {
	m := busyUIModel()
	aborted := false
	m.abort = func() { aborted = true }
	m.prompt.SetValue("/abort")
	m.prompt.rematch()

	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	require.True(t, aborted)
	require.Equal(t, "abort sent", m.notice.text)
}

func TestUI_TreeTool_OpensAndFocusesOutput(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi\n"), 0o644))
	t.Chdir(dir)

	m := testUIModel()
	nm, cmd := m.runSlash("/tree")
	m = nm.(Model)
	assert.Nil(t, cmd)
	assert.Equal(t, focusOutput, m.nav.focus)

	require.Len(t, m.blocks, 1)
	b := m.blocks[0]
	assert.Equal(t, "tree", b.tool)
	assert.True(t, b.ended)
	require.Len(t, b.steps, 1)
	row := b.steps[0]
	assert.Equal(t, "tree", row.toolKind)
	require.NotNil(t, row.tool.tree)

	v := m.View().Content
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
	nm, _ = m.outputKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = nm.(Model)
	require.Len(t, m.blocks, 1, "navigating the tree must not itself open anything")

	treeRow := m.blocks[0].steps[0]
	require.Equal(t, "a.txt", treeRow.tool.tree.Selected().Name)

	nm, _ = m.outputKey(tea.KeyPressMsg{Code: tea.KeyEnter})
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

	nm, _ = m.outputKey(tea.KeyPressMsg{Code: tea.KeyDown}) // -> sub
	m = nm.(Model)
	treeRow := m.blocks[0].steps[0]
	require.Equal(t, "sub", treeRow.tool.tree.Selected().Name)

	nm, _ = m.outputKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // toggle expand
	m = nm.(Model)
	require.Len(t, m.blocks, 1, "expanding a directory must not open a new block")
	assert.Contains(t, m.View().Content, "b.txt")
}

func TestUI_HelpTool_ListsSlashCommands(t *testing.T) {
	m := testUIModel()
	nm, _ := m.runSlash("/help")
	m = nm.(Model)

	v := m.View().Content
	assert.Contains(t, v, "/quit")
	assert.Contains(t, v, "/tree")
	assert.Contains(t, v, "/usage")
}

// /new empties the screen and the session behind it. The welcome pane
// is derived from there being no focused row, so clearing the blocks
// is what brings it back — there is no separate "show welcome" state
// that could disagree.
func TestUI_NewClearsTheSessionAndShowsWelcome(t *testing.T) {
	m := testUIModel()
	drv := m.sess.(*fakeDriver)

	m.prompt.SetValue("find the big files")
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	m.blocks[0].steps = []*stepRow{{command: "ls", cmd: cmdState{ec: &ExecutedCommand{Command: "ls"}}}}
	m.totalCmds = 1
	m.waiting = false
	m.nav.focus = focusOutput
	require.False(t, m.showWelcome(), "a row exists, so the welcome pane is gone")

	nm, cmd := m.runSlash("/new")
	m = nm.(Model)

	assert.Equal(t, 1, drv.resets, "the harness forgets the transcript too, not just the screen")
	assert.Empty(t, m.blocks)
	assert.Nil(t, m.cur)
	assert.Zero(t, m.totalCmds)
	assert.True(t, m.showWelcome(), "the welcome pane comes back")
	assert.True(t, m.prompt.Focused(), "and you can type straight away")
	assert.Equal(t, "new session", m.notice.text)
	assert.False(t, m.notice.bad)
	require.NotNil(t, cmd, "the welcome animation has to be re-armed; its ticker stopped at the first row")

	assert.Contains(t, plain(m.View().Content), "d e t e n t")
}

// Wiping the session mid-run would leave an in-flight command writing
// into a goal that no longer exists.
func TestUI_NewRefusesWhileBusy(t *testing.T) {
	m := busyUIModel()
	drv := m.sess.(*fakeDriver)

	nm, _ := m.runSlash("/new")
	m = nm.(Model)

	assert.Zero(t, drv.resets)
	assert.Contains(t, m.notice.text, "busy")
	assert.True(t, m.notice.bad)
}
