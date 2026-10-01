package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/event"
)

// Keystroke routing. handleKey computes exactly one owner for each
// key and hands it over; the owner's handler is the only thing that
// interprets it, so no two panes can claim the same key.

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m.onQuit("ctrl+c")
	}
	// Anything else means they are still working, so the quit they
	// half-asked for is no longer the next thing they meant.
	m.quitArmed = false
	if m.mode == modeUndo {
		return m.undoKey(msg)
	}
	if m.mode == modeForget {
		return m.forgetKey(msg)
	}
	if m.mode == modeBound {
		return m.boundKey(msg)
	}
	switch msg.String() {
	case "esc":
		return m.onEscape()
	case "tab":
		return m.onTab()
	case "shift+tab":
		return m.toggleEntry()
	}
	switch m.owner() {
	case ownerConfirm:
		return m.confirmKey(msg)
	case ownerInput, ownerBusy:
		return m.inputKey(msg)
	case ownerOutput:
		return m.outputKey(msg)
	default:
		return m.historyKey(msg)
	}
}

// handlePaste routes pasted text to whoever owns text entry, and
// nowhere otherwise: a paste into history or the output pane has no
// meaning, so it is dropped rather than going somewhere surprising.
func (m Model) handlePaste(text string) (tea.Model, tea.Cmd) {
	if text == "" {
		return m, nil
	}
	if m.mode != modeInput {
		return m, nil
	}
	switch m.owner() {
	case ownerInput, ownerBusy:
		m.prompt.Paste(text)
		m.clearNotice()
	}
	return m, nil
}

// keyOwner names who owns a keystroke; handleKey computes exactly
// one. The undo and bound questions are not owners: both take every
// key and short-circuit before owner() runs.
type keyOwner int

const (
	ownerConfirm keyOwner = iota
	ownerInput            // idle typing, slash dropdown included
	ownerBusy             // waiting: types like input, but esc aborts
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

// toggleEntry switches the bar between a request and a command.
// Focus follows, unless a question is up: that still comes first.
func (m Model) toggleEntry() (tea.Model, tea.Cmd) {
	m.entry = entryShell
	if m.prompt.shell {
		m.entry = entryPrompt
	}
	m.prompt.SetShell(m.entry == entryShell)
	if m.mode == modeInput {
		m.nav.focus = focusInput
		m.prompt.Focus()
	}
	m.clearNotice()
	return m, nil
}

// boundKey answers the step bound. The engine is paused, waiting.
func (m Model) boundKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.answerBound(true)
	case "n", "N", "esc":
		return m.answerBound(false)
	}
	return m, nil
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

// undoKey owns every key while the undo question is up: three
// outcomes, none of them implicit.
func (m Model) undoKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		return m.confirmUndo(true)
	case "n", "N", "enter":
		// enter takes the safe branch: the destructive answer has to
		// be typed deliberately.
		return m.confirmUndo(false)
	case "esc":
		return m.cancelUndo()
	case "up":
		m.output.ScrollUp(1)
		return m, nil
	case "down":
		m.output.ScrollDown(1)
		return m, nil
	case "pgup":
		m.output.HalfPageUp()
		return m, nil
	case "pgdown":
		m.output.HalfPageDown()
		return m, nil
	}
	return m, nil
}

// inputKey gives a focused idle input every keystroke: typing must
// never trigger navigation. Only pgup/pgdn and enter bypass it.
func (m Model) inputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if next, cmd, handled := m.slashKey(msg); handled {
		return next, cmd
	}
	switch msg.String() {
	case "enter":
		if m.entry == entryShell {
			return m.runShell()
		}
		return m.submit()
	case "pgup", "pgdown":
		return m.scrollViewport(msg.String())
	}
	cmd := m.prompt.Key(msg)
	m.clearNotice()
	return m, cmd
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
		next, cmd := m.submit()
		return next, cmd, true
	}
	return m, nil, false
}

// outputKey acts inside the detail component instead of moving rows.
func (m Model) outputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if r := m.focused(); r != nil && r.signin != nil {
		if next, cmd, ok := m.signInKey(r.signin, msg.String()); ok {
			return next, cmd
		}
	}
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
			if msg.String() == "enter" {
				if nm, ok := m.seedFromView(r); ok {
					return nm, nil
				}
			}
			m.toggleExpand(r)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.output, cmd = m.output.Update(msg)
	return m, cmd
}

func (m Model) historyKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if r := m.focused(); r != nil && r.signin != nil {
		if next, cmd, ok := m.signInKey(r.signin, msg.String()); ok {
			return next, cmd
		}
	}
	switch msg.String() {
	case "up":
		return m.navUp()
	case "down":
		return m.navDown()
	case "pgup", "pgdown":
		return m.scrollViewport(msg.String())
	case "enter":
		if r := m.focused(); r != nil && !r.running {
			m.toggleExpand(r)
		}
		return m, nil
	case "v", "space":
		if r := m.focused(); r != nil {
			m.toggleExpand(r)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.output, cmd = m.output.Update(msg)
	return m, cmd
}

// onEscape backs out of the innermost thing first: dropdown, then a
// running request, then the output pane. Intercepted before owner(),
// so anything wanting esc is handled here.
func (m Model) onEscape() (tea.Model, tea.Cmd) {
	if m.mode == modeConfirm {
		return m.decline()
	}
	if m.prompt.Open() {
		m.prompt.Close()
		return m, nil
	}
	if m.closePanel() {
		return m, nil
	}
	// Idle esc in the output pane steps back to history; a running
	// request still aborts.
	if m.nav.focus == focusOutput && m.cur == nil {
		m.nav.focus = focusHistory
		return m, nil
	}
	// A command the human ran stops before the Turn does: it is theirs,
	// and they are watching it.
	if m.shellRunning() {
		return m, m.send(event.CancelCommand{})
	}
	if m.cur != nil {
		return m, m.send(event.Abort{Turn: m.cur.id})
	}
	return m, nil
}

// toggleExpand opens or shuts a row's preview. The only thing outside
// apply that changes what history draws, so the only other bump.
func (m *Model) toggleExpand(r *callRow) {
	r.expanded = !r.expanded
	m.histRev++
}

// forgetKey answers the delete question. Every key but y cancels.
func (m Model) forgetKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		return m.confirmForget()
	case "up":
		m.output.ScrollUp(1)
		return m, nil
	case "down":
		m.output.ScrollDown(1)
		return m, nil
	}
	return m.cancelForget()
}
