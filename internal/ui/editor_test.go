package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/agentloop"
	"github.com/vitzeno/detent/internal/shell"
	"github.com/vitzeno/detent/internal/ui/editor"
	"github.com/vitzeno/detent/internal/usage"
)

func TestUI_EditorOpensAfterEditedFileCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.py")
	require.NoError(t, os.WriteFile(path, []byte("print(1)\n"), 0o644))

	m := testUIModel()
	res := &agentloop.GoalResult{Goal: "g"}
	m.blocks = []*goalBlock{{goal: "g", res: res}}
	m.cur = m.blocks[0]
	row := &stepRow{command: "cat > " + path, editPath: path, running: true, usage: &usage.Step{}}
	m.cur.steps = append(m.cur.steps, row)

	nm, cmd := m.onExecDone(execDoneMsg{ec: &agentloop.ExecutedCommand{Command: row.command, Result: shell.Result{ExitCode: 0}}})
	m = nm.(Model)
	require.NotNil(t, cmd, "must still chain into judge+propose like any other command")

	require.NotNil(t, row.editor)
	require.NoError(t, row.editor.Err())
	assert.Equal(t, "print(1)\n", row.editor.Value())
	assert.False(t, row.editor.Dirty())
}

func TestUI_EditorLoadErrorIsShownNotFatal(t *testing.T) {
	m := testUIModel()
	res := &agentloop.GoalResult{Goal: "g"}
	m.blocks = []*goalBlock{{goal: "g", res: res}}
	m.cur = m.blocks[0]
	missing := filepath.Join(t.TempDir(), "does-not-exist.txt")
	row := &stepRow{command: "cmd", editPath: missing, running: true, usage: &usage.Step{}}
	m.cur.steps = append(m.cur.steps, row)

	nm, _ := m.onExecDone(execDoneMsg{ec: &agentloop.ExecutedCommand{Command: row.command}})
	m = nm.(Model)

	require.NotNil(t, row.editor)
	assert.Error(t, row.editor.Err())
	m.cursor = 0
	assert.Contains(t, m.detailLines()[0], "could not open")
}

func rowWithEditor(t *testing.T, initial string) (*stepRow, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.txt")
	require.NoError(t, os.WriteFile(path, []byte(initial), 0o644))
	ed := editor.New(path)
	return &stepRow{command: "cmd", editPath: path, editor: &ed}, path
}

func TestUI_EnterAndExitEditMode(t *testing.T) {
	row, _ := rowWithEditor(t, "hi\n")
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{row}}}
	m.focus = focusOutput
	m.cursor = 0

	nm, cmd := m.outputKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	assert.True(t, m.editing)
	assert.NotNil(t, cmd, "Focus() returns a blink cmd")

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	assert.False(t, m.editing, "esc leaves edit mode")
}

func TestUI_EditingUpdatesBuffer(t *testing.T) {
	row, _ := rowWithEditor(t, "hi\n")
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{row}}}
	m.focus = focusOutput
	m.cursor = 0
	m.editing = true
	row.editor.Focus()

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	m = nm.(Model)
	assert.Contains(t, row.editor.Value(), "X")
	assert.True(t, row.editor.Dirty())
}

func TestUI_SaveFlow(t *testing.T) {
	row, path := rowWithEditor(t, "old\n")
	sess := testSession()
	m := testUIModel()
	m.sess = sess
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{row}}}
	m.focus = focusOutput
	m.cursor = 0
	m.editing = true
	row.editor.SetValue("new\n")

	nm, cmd := m.startSave()
	m = nm.(Model)
	require.Equal(t, modeSaveConfirm, m.mode)
	require.Same(t, row, m.saveRow)
	assert.Nil(t, cmd)

	diffView := m.saveConfirmBox()
	assert.Contains(t, diffView, "-old")
	assert.Contains(t, diffView, "+new")

	nm, cmd = m.confirmSave()
	m = nm.(Model)
	require.Equal(t, modeInput, m.mode)
	require.NotNil(t, cmd)

	msg := cmd()
	saveMsg, ok := msg.(saveDoneMsg)
	require.True(t, ok)
	require.NoError(t, saveMsg.err)

	nm, _ = m.onSaveDone(saveMsg)
	m = nm.(Model)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(got))
	assert.False(t, row.editor.Dirty())

	require.Len(t, sess.Transcript, 1, "a save is recorded in the transcript, not run as a command")
	assert.Contains(t, sess.Transcript[0].Content, path)
	assert.Contains(t, sess.Transcript[0].Content, "-old")
	assert.Contains(t, sess.Transcript[0].Content, "+new")
}

func TestUI_SaveNothingToSaveShowsNotice(t *testing.T) {
	row, _ := rowWithEditor(t, "same\n")
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{row}}}
	m.focus = focusOutput
	m.cursor = 0

	nm, cmd := m.startSave()
	m = nm.(Model)
	assert.Nil(t, cmd)
	assert.NotEqual(t, modeSaveConfirm, m.mode)
	assert.Equal(t, "nothing to save", m.notice)
}

func TestUI_CancelSaveKeepsBufferAndDoesNotWrite(t *testing.T) {
	row, path := rowWithEditor(t, "old\n")
	row.editor.SetValue("new\n")
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{row}}}
	m.focus = focusOutput
	m.cursor = 0
	m.saveRow = row
	m.mode = modeSaveConfirm

	nm, cmd := m.saveConfirmKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = nm.(Model)
	assert.Nil(t, cmd)
	assert.Equal(t, modeInput, m.mode)
	assert.Nil(t, m.saveRow)
	assert.Equal(t, "new\n", row.editor.Value(), "declining a save must not discard the edit")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "old\n", string(got), "declining a save must not write to disk")
}
