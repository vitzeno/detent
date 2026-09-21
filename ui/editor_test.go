package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/ui/editor"
)

func TestUI_EditorOpensAfterEditedFileCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.py")
	require.NoError(t, os.WriteFile(path, []byte("print(1)\n"), 0o644))

	m := testUIModel()
	res := &GoalResult{Goal: "g"}
	m.blocks = []*goalBlock{{goal: "g", res: res}}
	m.cur = m.blocks[0]
	row := &stepRow{command: "cat > " + path, editPath: path, cmd: cmdState{running: true}}
	m.cur.steps = append(m.cur.steps, row)

	nm, cmd := m.onExecDone(execDoneMsg{ec: &ExecutedCommand{Command: row.command, Result: Result{ExitCode: 0}}})
	m = nm.(Model)
	require.NotNil(t, cmd, "must still chain into judge+propose like any other command")

	require.NotNil(t, row.editor)
	require.NoError(t, row.editor.Err())
	assert.Equal(t, "print(1)\n", row.editor.Value())
	assert.False(t, row.editor.Dirty())
}

func TestUI_EditorLoadErrorIsShownNotFatal(t *testing.T) {
	m := testUIModel()
	m.sess = &fakeDriver{readOverride: true, readErr: os.ErrNotExist}
	res := &GoalResult{Goal: "g"}
	m.blocks = []*goalBlock{{goal: "g", res: res}}
	m.cur = m.blocks[0]
	row := &stepRow{command: "cmd", editPath: "does-not-exist.txt", cmd: cmdState{running: true}}
	m.cur.steps = append(m.cur.steps, row)

	nm, _ := m.onExecDone(execDoneMsg{ec: &ExecutedCommand{Command: row.command}})
	m = nm.(Model)

	require.NotNil(t, row.editor)
	assert.Error(t, row.editor.Err())
	m.nav.cursor = 0
	assert.Contains(t, m.detailLines()[0], "could not open")
}

func rowWithEditor(t *testing.T, initial string) (*stepRow, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.txt")
	require.NoError(t, os.WriteFile(path, []byte(initial), 0o644))
	ed := editor.New(path, initial, false, 0, nil)
	return &stepRow{command: "cmd", editPath: path, editor: &ed}, path
}

func TestUI_EnterAndExitEditMode(t *testing.T) {
	row, _ := rowWithEditor(t, "hi\n")
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{row}}}
	m.nav.focus = focusOutput
	m.nav.cursor = 0

	nm, cmd := m.outputKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	assert.True(t, m.save.editing)
	assert.NotNil(t, cmd, "Focus() returns a blink cmd")

	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = nm.(Model)
	assert.False(t, m.save.editing, "esc leaves edit mode")
}

func TestUI_EditingUpdatesBuffer(t *testing.T) {
	row, _ := rowWithEditor(t, "hi\n")
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{row}}}
	m.nav.focus = focusOutput
	m.nav.cursor = 0
	m.save.editing = true
	row.editor.Focus()

	nm, _ := m.Update(tea.KeyPressMsg{Code: 'X', Text: "X"})
	m = nm.(Model)
	assert.Contains(t, row.editor.Value(), "X")
	assert.True(t, row.editor.Dirty())
}

func TestUI_SaveFlow(t *testing.T) {
	row, path := rowWithEditor(t, "old\n")
	drv := newFakeDriver()
	m := testUIModel()
	m.sess = drv
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{row}}}
	m.nav.focus = focusOutput
	m.nav.cursor = 0
	m.save.editing = true
	row.editor.SetValue("new\n")

	nm, cmd := m.startSave()
	m = nm.(Model)
	require.Equal(t, modeSaveConfirm, m.mode)
	require.Same(t, row, m.save.row)
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

	assert.False(t, row.editor.Dirty())

	assert.Equal(t, path, drv.savedPath, "SaveFile must be called with the row's own path")
	assert.Equal(t, "new\n", drv.savedContent)
	assert.Contains(t, drv.savedDiff, "-old")
	assert.Contains(t, drv.savedDiff, "+new")
}

func TestUI_SaveNothingToSaveShowsNotice(t *testing.T) {
	row, _ := rowWithEditor(t, "same\n")
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{row}}}
	m.nav.focus = focusOutput
	m.nav.cursor = 0

	nm, cmd := m.startSave()
	m = nm.(Model)
	assert.Nil(t, cmd)
	assert.NotEqual(t, modeSaveConfirm, m.mode)
	assert.Equal(t, "nothing to save", m.notice.text)
}

func TestUI_CancelSaveKeepsBufferAndDoesNotWrite(t *testing.T) {
	row, path := rowWithEditor(t, "old\n")
	row.editor.SetValue("new\n")
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{row}}}
	m.nav.focus = focusOutput
	m.nav.cursor = 0
	m.save.row = row
	m.mode = modeSaveConfirm

	nm, cmd := m.saveConfirmKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = nm.(Model)
	assert.Nil(t, cmd)
	assert.Equal(t, modeInput, m.mode)
	assert.Nil(t, m.save.row)
	assert.Equal(t, "new\n", row.editor.Value(), "declining a save must not discard the edit")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "old\n", string(got), "declining a save must not write to disk")
}

// A command that failed usually never wrote the file it named, and
// the editor takes the whole output pane — so "could not open x" used
// to replace the stderr that explained what went wrong.
func TestUI_FailedCommandShowsOutputNotAFileError(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", res: &GoalResult{Goal: "g"}}}
	m.cur = m.blocks[0]
	row := &stepRow{command: "cat > test.py <<'EOF'", editPath: "test.py",
		cmd: cmdState{running: true}}
	m.cur.steps = []*stepRow{row}
	m.sizeViewport()

	failed := &ExecutedCommand{
		Command: "cat > test.py <<'EOF'",
		Result:  Result{ExitCode: 2, Stderr: "sh: syntax error: unexpected end of file\n"},
	}
	nm, _ := m.Update(execDoneMsg{ec: failed})
	m = nm.(Model)

	assert.Nil(t, row.editor, "no editor for a file the command never wrote")
	out := plain(m.View().Content)
	assert.Contains(t, out, "syntax error", "the failure itself is what a human needs")
	assert.NotContains(t, out, "could not open")
}

// A command that worked still opens its file.
func TestUI_SucceededCommandOpensItsFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("made.py", []byte("print(1)\n"), 0o644))

	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", res: &GoalResult{Goal: "g"}}}
	m.cur = m.blocks[0]
	row := &stepRow{command: "cat > made.py", editPath: "made.py", cmd: cmdState{running: true}}
	m.cur.steps = []*stepRow{row}

	nm, _ := m.Update(execDoneMsg{ec: &ExecutedCommand{Command: "cat > made.py", Result: Result{ExitCode: 0}}})
	_ = nm
	require.NotNil(t, row.editor)
	assert.Equal(t, "print(1)\n", row.editor.Value())
}
