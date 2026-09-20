package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Keystroke routing. handleKey computes exactly one owner for each
// key and hands it over; the owner's handler is the only thing that
// interprets it, so no two panes can claim the same key.

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.mode == modeSaveConfirm {
		return m.saveConfirmKey(msg)
	}
	if m.save.editing {
		return m.editorKey(msg)
	}
	switch msg.String() {
	case "esc":
		return m.onEscape()
	case "tab":
		return m.onTab()
	}
	switch m.owner() {
	case ownerConfirm:
		return m.confirmKey(msg)
	case ownerInput:
		return m.inputKey(msg)
	case ownerBusy:
		return m.busyKey(msg)
	case ownerOutput:
		return m.outputKey(msg)
	default:
		return m.historyKey(msg)
	}
}

// keyOwner names who owns a keystroke; handleKey computes exactly one.
// Editing and modeSaveConfirm aren't keyOwner values — both take over
// every key unconditionally and short-circuit before owner() runs.
type keyOwner int

const (
	ownerConfirm keyOwner = iota
	ownerInput            // idle typing, slash dropdown included
	ownerBusy             // waiting: slash entry only
	ownerOutput
	ownerHistory
)

func (m Model) owner() keyOwner {
	if m.mode == modeConfirm {
		return ownerConfirm
	}
	if m.nav.focus == focusOutput {
		return ownerOutput
	}
	if m.nav.focus == focusHistory || !m.prompt.Focused() {
		return ownerHistory
	}
	if m.waiting {
		return ownerBusy
	}
	return ownerInput
}

// onTab completes an open dropdown, otherwise cycles panes.
func (m Model) onTab() (tea.Model, tea.Cmd) {
	if m.mode == modeConfirm {
		return m, nil
	}
	if m.nav.focus == focusInput && m.mode == modeInput && m.prompt.Focused() && m.prompt.Open() {
		return m.acceptSlash()
	}
	return m.toggleFocus()
}

func (m Model) confirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.approve()
	case "n", "N":
		return m.decline()
	}
	return m, nil
}

func (m Model) saveConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.confirmSave()
	case "n", "N", "esc":
		return m.cancelSave()
	}
	return m, nil
}

// editorKey owns every key while editing: esc leaves edit mode without
// touching disk, ctrl+s opens the diff confirm, everything else goes to
// the textarea.
func (m Model) editorKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	r := m.focused()
	if r == nil || r.editor == nil {
		m.save.editing = false
		return m, nil
	}
	switch msg.String() {
	case "esc":
		r.editor.Blur()
		m.save.editing = false
		return m, nil
	case "ctrl+s":
		return m.startSave()
	}
	var cmd tea.Cmd
	*r.editor, cmd = r.editor.Update(msg)
	return m, cmd
}

// inputKey gives a focused idle input every keystroke — typing must
// never trigger navigation. Only pgup/pgdn and enter bypass the input.
func (m Model) inputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if next, _, handled := m.slashKey(msg); handled {
		return next, nil
	}
	switch msg.String() {
	case "enter":
		return m.startGoal()
	case "pgup", "pgdown":
		return m.scrollViewport(msg.String())
	}
	cmd := m.prompt.Key(msg)
	m.clearNotice()
	return m, cmd
}

// busyKey narrows a focused waiting input to slash entry: plain goals
// can't start mid-run, but /abort and /quit stay reachable.
func (m Model) busyKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if next, _, handled := m.slashKey(msg); handled {
		return next, nil
	}
	switch msg.String() {
	case "pgup", "pgdown":
		return m.scrollViewport(msg.String())
	}
	// Only "/" may start slash entry; once typed, all keys go to input.
	if strings.HasPrefix(m.prompt.Value(), "/") ||
		(msg.String() == "/" && m.prompt.Value() == "") {
		cmd := m.prompt.Key(msg)
		m.clearNotice()
		return m, cmd
	}
	return m, nil
}

// slashKey drives the open dropdown (arrows/enter/esc); tab is handled
// in onTab. Reports handled=false when no dropdown is open.
func (m Model) slashKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if !m.prompt.Open() {
		return m, nil, false
	}
	switch msg.String() {
	case "up":
		m.prompt.Move(-1)
		return m, nil, true
	case "down":
		m.prompt.Move(1)
		return m, nil, true
	case "enter":
		// Enter runs the highlighted entry; tab is what completes
		// without running. Completing on enter meant every command
		// took two presses, which is not how the dropdown reads.
		m.prompt.Accept()
		next, cmd := m.startGoal()
		return next, cmd, true
	case "esc":
		m.prompt.Close()
		return m, nil, true
	}
	return m, nil, false
}

// outputKey acts inside the detail component instead of moving rows.
func (m Model) outputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up":
		return m.outputNav(-1)
	case "down":
		return m.outputNav(1)
	case "pgup":
		m.output.HalfPageUp()
		return m, nil
	case "pgdown":
		m.output.HalfPageDown()
		return m, nil
	case "enter", "v", "space":
		if r := m.focused(); r != nil {
			if r.editor != nil {
				m.save.editing = true
				return m, r.editor.Focus()
			}
			switch r.toolKind {
			case "tree":
				return m.openTreeSelection(r)
			case "usage":
				if r.tool.usageExpand == r.tool.usageCursor {
					r.tool.usageExpand = -1
				} else {
					r.tool.usageExpand = r.tool.usageCursor
				}
				return m, nil
			}
			r.cmd.expanded = !r.cmd.expanded
		}
		return m, nil
	case "q":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.output, cmd = m.output.Update(msg)
	return m, cmd
}

func (m Model) historyKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up":
		return m.navUp()
	case "down":
		return m.navDown()
	case "pgup", "pgdown":
		return m.scrollViewport(msg.String())
	case "enter":
		if r := m.focused(); r != nil && !r.cmd.running {
			r.cmd.expanded = !r.cmd.expanded
		}
		return m, nil
	case "v", "space":
		if r := m.focused(); r != nil {
			r.cmd.expanded = !r.cmd.expanded
		}
		return m, nil
	case "q":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.output, cmd = m.output.Update(msg)
	return m, cmd
}

func (m Model) onEscape() (tea.Model, tea.Cmd) {
	if m.mode == modeConfirm {
		return m.decline()
	}
	// Idle esc in the output pane steps back to history; a running
	// command still aborts.
	if m.nav.focus == focusOutput && m.abort == nil {
		m.nav.focus = focusHistory
		return m, nil
	}
	if m.abort != nil {
		m.abort()
		m.abort = nil
	}
	return m, nil
}
