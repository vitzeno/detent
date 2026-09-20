package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vitzeno/detent/ui/tabular"
)

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

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

func (m Model) confirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.approve()
	case "n", "N":
		return m.decline()
	}
	return m, nil
}

func (m Model) saveConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
func (m Model) editorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
func (m Model) inputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	m.notice = ""
	return m, cmd
}

// busyKey narrows a focused waiting input to slash entry: plain goals
// can't start mid-run, but /abort and /quit stay reachable.
func (m Model) busyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		m.notice = ""
		return m, cmd
	}
	return m, nil
}

// slashKey drives the open dropdown (arrows/enter/esc); tab is handled
// in onTab. Reports handled=false when no dropdown is open.
func (m Model) slashKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
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
		if !m.prompt.IsExactCommand() {
			next, cmd := m.acceptSlash()
			return next, cmd, true
		}
		next, cmd := m.startGoal()
		return next, cmd, true
	case "esc":
		m.prompt.Close()
		return m, nil, true
	}
	return m, nil, false
}

// outputKey acts inside the detail component instead of moving rows.
func (m Model) outputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up":
		return m.outputNav(-1)
	case "down":
		return m.outputNav(1)
	case "pgup":
		m.output.HalfViewUp()
		return m, nil
	case "pgdown":
		m.output.HalfViewDown()
		return m, nil
	case "enter", "v", " ":
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

func (m Model) historyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	case "v", " ":
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

// outputNav moves inside the detail component: the table cursor for
// tabular rows, viewport lines otherwise.
func (m Model) outputNav(d int) (tea.Model, tea.Cmd) {
	if r := m.focused(); r != nil && !r.cmd.running {
		if src, ok := r.tableText(); ok {
			if _, rows, ok := tabular.Parse(src, m.output.Width); ok && len(rows) > 0 {
				r.cmd.tableCursor = min(max(r.cmd.tableCursor+d, 0), len(rows)-1)
				return m, nil
			}
		}
		switch r.toolKind {
		case "tree":
			if r.tool.tree != nil {
				if d < 0 {
					r.tool.tree.Up()
				} else {
					r.tool.tree.Down()
				}
			}
			return m, nil
		case "usage":
			n := len(m.sess.Tracker())
			if d < 0 && r.tool.usageCursor > 0 {
				r.tool.usageCursor--
			}
			if d > 0 && r.tool.usageCursor < n-1 {
				r.tool.usageCursor++
			}
			return m, nil
		}
	}
	if d < 0 {
		m.output.LineUp(1)
	} else {
		m.output.LineDown(1)
	}
	return m, nil
}

func (m Model) navUp() (tea.Model, tea.Cmd) {
	if m.nav.cursor > 0 {
		m.nav.cursor--
		m.nav.follow = false
	}
	return m, nil
}

func (m Model) navDown() (tea.Model, tea.Cmd) {
	rows := m.rows()
	if m.nav.cursor < len(rows)-1 {
		m.nav.cursor++
		if m.nav.cursor == len(rows)-1 {
			m.nav.follow = true
		}
	}
	return m, nil
}

func (m Model) scrollViewport(key string) (tea.Model, tea.Cmd) {
	if key == "pgup" {
		m.output.HalfViewUp()
	} else {
		m.output.HalfViewDown()
	}
	return m, nil
}

// toggleFocus cycles input → history → output → input. Arrows act in
// whichever pane is focused.
func (m Model) toggleFocus() (tea.Model, tea.Cmd) {
	switch m.nav.focus {
	case focusInput:
		m.nav.focus = focusHistory
		m.prompt.Blur()
	case focusHistory:
		m.nav.focus = focusOutput
	default:
		m.nav.focus = focusInput
		m.prompt.Focus()
	}
	return m, nil
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
