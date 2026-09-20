package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// startSave opens the diff confirm for the focused row's editor. A
// no-op (with a notice, not a confirm nobody needs) when there's
// nothing to save.
func (m Model) startSave() (tea.Model, tea.Cmd) {
	r := m.focused()
	if r == nil || r.editor == nil || !r.editor.Dirty() {
		m.notice = "nothing to save"
		return m, nil
	}
	m.save.row = r
	m.mode = modeSaveConfirm
	m.sizeViewport()
	return m, nil
}

func (m Model) confirmSave() (tea.Model, tea.Cmd) {
	m.mode = modeInput
	r := m.save.row
	m.save.row = nil
	m.sizeViewport()
	if r == nil || r.editor == nil {
		return m, nil
	}
	return m, saveCmd(m.sess, r, r.editor.Path, r.editor.Value())
}

func (m Model) cancelSave() (tea.Model, tea.Cmd) {
	m.mode = modeInput
	m.save.row = nil
	m.sizeViewport()
	return m, nil
}

// onSaveDone lands once the write (and its transcript note) finish.
// The buffer itself is untouched either way — a failed save leaves the
// edit exactly as it was, free to retry.
func (m Model) onSaveDone(msg saveDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.notice = fmt.Sprintf("save failed: %v", msg.err)
		return m, nil
	}
	if msg.row != nil && msg.row.editor != nil {
		msg.row.editor.MarkSaved(msg.content)
	}
	m.notice = "saved"
	return m, nil
}
