package ui

import (
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// Keystroke routing. handleKey hands each key to exactly one owner,
// so no two panes can claim the same key.

// keyOwner names who owns a keystroke. The undo, delete and bound questions
// and the finder are not owners: they take every key before owner() runs.
type keyOwner int

const (
	ownerConfirm keyOwner = iota
	ownerInput            // idle typing, slash dropdown included
	ownerBusy             // waiting: types like input, but esc aborts
	ownerOutput
	ownerHistory
)

// pressed is handleKey with any key but esc dropping a stop esc had asked for,
// so only two esc in a row stop a request.
func (m Model) pressed(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if !key.Matches(msg, keymap.app.esc) {
		m.escArmed = time.Time{}
	}
	return m.handleKey(msg)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if key.Matches(msg, keymap.app.quit) {
		return m.onQuit("ctrl+c")
	}
	// Anything else means they are still working, so the quit they
	// half-asked for is no longer the next thing they meant.
	m.quitArmed = false
	if !key.Matches(msg, keymap.history.stop) {
		m.stopArmed = uuid.Nil
	}
	if m.mode == modeUndo {
		return m.undoKey(msg)
	}
	if m.mode == modeForget {
		return m.forgetKey(msg)
	}
	if m.mode == modeBound {
		return m.boundKey(msg)
	}
	if m.modal != nil {
		cmd := m.modal.key(&m, msg)
		return m, cmd
	}
	switch {
	case key.Matches(msg, keymap.app.find):
		return m.openFinder("")
	case key.Matches(msg, keymap.app.esc):
		return m.onEscape()
	case key.Matches(msg, keymap.app.tab):
		return m.onTab()
	case key.Matches(msg, keymap.app.entry):
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

// handlePaste routes pasted text to whoever owns text entry. A paste
// anywhere else has no meaning, so it is dropped.
func (m Model) handlePaste(text string) (Model, tea.Cmd) {
	if text == "" {
		return m, nil
	}
	if f := modalAs[*finderModal](m); f != nil {
		f.add(m, text)
		return m, nil
	}
	if r := modalAs[*reviewModal](m); r != nil && r.edit != nil {
		r.edit.input.InsertString(text)
		return m, nil
	}
	if m.mode != modeInput {
		return m, nil
	}
	switch m.owner() {
	case ownerInput, ownerBusy:
		m.prompt.Paste(text)
		m.clearNotice()
	case ownerConfirm, ownerOutput, ownerHistory:
	}
	return m, nil
}

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

// undoKey owns every key while the undo question is up: three
// outcomes, none of them implicit.
func (m Model) undoKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := keymap.ask
	// With no files to revert there is one question, not two.
	if b := m.undo.target; b != nil && !b.files {
		switch {
		case key.Matches(msg, k.yes, k.enter):
			return m.confirmUndo(false)
		case key.Matches(msg, k.no):
			return m.cancelUndo()
		}
	}
	switch {
	case key.Matches(msg, k.yes):
		return m.confirmUndo(true)
	case key.Matches(msg, k.no, k.enter):
		// enter takes the safe branch: the destructive answer has to
		// be typed deliberately.
		return m.confirmUndo(false)
	case key.Matches(msg, k.esc):
		return m.cancelUndo()
	}
	m.scrollOutput(k.read, msg)
	return m, nil
}

// forgetKey answers the delete question. Every key but y cancels.
func (m Model) forgetKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if key.Matches(msg, keymap.ask.yes) {
		return m.confirmForget()
	}
	if m.scrollOutput(keymap.ask.read, msg) {
		return m, nil
	}
	return m.cancelForget()
}

// boundKey answers the step bound. The engine is paused, waiting.
func (m Model) boundKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.settling() {
		return m, nil
	}
	switch {
	case key.Matches(msg, keymap.ask.yes, keymap.ask.enter):
		return m.answerBound(true)
	// Not esc: stopping a request takes a deliberate key, never a stray one.
	case key.Matches(msg, keymap.ask.no):
		return m.answerBound(false)
	}
	return m, nil
}

// onEscape backs out of the innermost thing first: question, dropdown,
// panel, output pane, then a running command or request.
func (m Model) onEscape() (Model, tea.Cmd) {
	if m.mode == modeConfirm {
		// Settles like y and n, and goes no further: a later branch aborts the Turn.
		if m.settling() {
			return m, nil
		}
		return m.decline()
	}
	if m.prompt.Open() {
		m.prompt.Close()
		return m, nil
	}
	if m.closePanel() {
		return m, nil
	}
	// Idle esc in the output pane steps back to history. A running
	// request still aborts.
	if m.nav.focus == focusOutput && m.cur == nil {
		m.nav.focus = focusHistory
		return m, nil
	}
	if !m.userCommandRunning() && m.cur == nil {
		return m, nil
	}
	// One esc only asks: a second soon after stops it.
	if !m.escStops() {
		m.escArmed = time.Now()
		return m, nil
	}
	m.escArmed = time.Time{}
	// A command the human ran stops before the Turn does: it is theirs,
	// and they are watching it.
	if m.userCommandRunning() {
		m.send(event.CancelCommand{})
		return m, nil
	}
	return m.abortRunning()
}

// escStops is whether one esc has asked to stop what runs, and a second would.
func (m Model) escStops() bool { return time.Since(m.escArmed) < escTwice }

// onTab completes an open dropdown, otherwise cycles panes.
func (m Model) onTab() (Model, tea.Cmd) {
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
func (m Model) toggleEntry() (Model, tea.Cmd) {
	m.prompt.SetShell(!m.prompt.shell)
	if m.mode == modeInput {
		m.nav.focus = focusInput
		m.prompt.Focus()
	}
	m.clearNotice()
	return m, nil
}

// confirmKey answers an approval. y waits until the whole command has
// been on screen, since approving a tail nobody saw is no approval.
func (m Model) confirmKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := keymap.ask
	// Too soon to be an answer: likely the next key of something typed.
	if key.Matches(msg, k.yes, k.enter, k.no) && m.settling() {
		return m, nil
	}
	switch {
	case key.Matches(msg, k.yes, k.enter):
		if !m.confirmReady() {
			m.noteErr("read to the end of the command first: ↓ scrolls it")
			return m, nil
		}
		return m.approve()
	case key.Matches(msg, k.no):
		return m.decline()
	}
	_, room := m.confirmLines()
	if d, ok := k.read.delta(msg, room); ok {
		m.scrollConfirm(d)
	}
	return m, nil
}

// inputKey gives a focused idle input every keystroke: typing must
// never trigger navigation. Only pgup/pgdn and enter bypass it.
func (m Model) inputKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if next, cmd, handled := m.slashKey(msg); handled {
		return next, cmd
	}
	switch {
	case key.Matches(msg, keymap.input.run):
		if m.prompt.shell {
			return m.runShell()
		}
		return m.submit()
	case key.Matches(msg, keymap.input.pageUp, keymap.input.pageDown):
		return m.scrollViewport(msg)
	}
	cmd := m.prompt.Key(msg)
	m.clearNotice()
	return m, cmd
}

// slashKey drives the open dropdown's arrows and enter. Reports
// handled=false when no dropdown is open.
func (m Model) slashKey(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	if !m.prompt.Open() {
		return m, nil, false
	}
	switch {
	case key.Matches(msg, keymap.input.pick.up):
		m.prompt.Move(-1)
		return m, nil, true
	case key.Matches(msg, keymap.input.pick.down):
		m.prompt.Move(1)
		return m, nil, true
	case key.Matches(msg, keymap.input.run):
		// Enter runs the highlighted entry, tab completes without running. In a
		// sentence enter only completes, since the rest is still to be typed.
		if m.prompt.MidSentence() {
			m.prompt.Accept()
			return m, nil, true
		}
		m.prompt.Accept()
		next, cmd := m.submit()
		return next, cmd, true
	}
	return m, nil, false
}

// outputKey acts inside the detail component instead of moving rows.
func (m Model) outputKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if key.Matches(msg, keymap.history.agents) && len(m.agentOrder) > 0 {
		return m.showAgents("")
	}
	if r := m.focused(); r != nil && r.signin != nil {
		if next, cmd, ok := m.signInKey(r.signin, msg); ok {
			return next, cmd
		}
	}
	k := keymap.output
	switch {
	case key.Matches(msg, k.move.pageUp):
		m.output.HalfPageUp()
		return m, nil
	case key.Matches(msg, k.move.pageDown):
		m.output.HalfPageDown()
		return m, nil
	case key.Matches(msg, k.expand):
		if r := m.focused(); r != nil {
			if key.Matches(msg, keymap.input.run) {
				if nm, ok := m.seedFromView(r); ok {
					return nm, nil
				}
			}
			m.toggleExpand(r)
		}
		return m, nil
	}
	if d, ok := k.move.delta(msg, 1); ok {
		return m.outputNav(d)
	}
	// The viewport's own keys, j and k among them, scroll it too.
	var cmd tea.Cmd
	m.output, cmd = m.output.Update(msg)
	return m, cmd
}

func (m Model) historyKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := keymap.history
	if key.Matches(msg, k.agents) && len(m.agentOrder) > 0 {
		return m.showAgents("")
	}
	if r := m.focused(); r != nil && r.signin != nil {
		if next, cmd, ok := m.signInKey(r.signin, msg); ok {
			return next, cmd
		}
	}
	switch {
	case key.Matches(msg, k.move.up):
		return m.navUp()
	case key.Matches(msg, k.move.down):
		return m.navDown()
	case key.Matches(msg, k.move.top):
		return m.navTop()
	case key.Matches(msg, k.move.bottom):
		m.followNewest()
		return m, nil
	case key.Matches(msg, k.move.pageUp, k.move.pageDown):
		return m.scrollViewport(msg)
	case key.Matches(msg, k.open):
		// A review's row has its reviewer too, but what it stands for is the review.
		if r := m.focused(); r != nil && r.review != uuid.Nil {
			return m.openReviewRow(r)
		}
		if r := m.focused(); r != nil && r.agent != nil {
			return m.openInspector(r.agent)
		}
		if r := m.focused(); r != nil && !r.running {
			m.toggleExpand(r)
		}
		return m, nil
	case key.Matches(msg, k.expand):
		if r := m.focused(); r != nil {
			m.toggleExpand(r)
		}
		return m, nil
	case key.Matches(msg, k.stop):
		if r := m.focused(); r != nil {
			return m.stopAgent(r.agent)
		}
	}
	return m, nil
}

// scrollOutput scrolls the output pane if msg is one of k, half a pane to a
// page, and reports whether it was.
func (m *Model) scrollOutput(k moveKeys, msg tea.KeyPressMsg) bool {
	d, ok := k.delta(msg, max(1, m.output.Height()/2))
	switch {
	case !ok:
	case d < 0:
		m.output.ScrollUp(-d)
	default:
		m.output.ScrollDown(d)
	}
	return ok
}
