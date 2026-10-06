package ui

import (
	"time"

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
	if msg.String() != "esc" {
		m.escArmed = time.Time{}
	}
	return m.handleKey(msg)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m.onQuit("ctrl+c")
	}
	// Anything else means they are still working, so the quit they
	// half-asked for is no longer the next thing they meant.
	m.quitArmed = false
	if msg.String() != "x" {
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
	if m.mode == modeFinder {
		return m.finderKey(msg)
	}
	if m.mode == modeInspector {
		return m.inspectorKey(msg)
	}
	if m.mode == modeResume {
		return m.resumeKey(msg)
	}
	if m.mode == modeReview {
		return m.reviewKey(msg)
	}
	switch msg.String() {
	case "ctrl+f":
		return m.openFinder("")
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

// handlePaste routes pasted text to whoever owns text entry. A paste
// anywhere else has no meaning, so it is dropped.
func (m Model) handlePaste(text string) (Model, tea.Cmd) {
	if text == "" {
		return m, nil
	}
	if m.mode == modeFinder {
		m.finder.query += oneLine(text)
		m.refreshFinder()
		return m, nil
	}
	if e := m.review.edit; m.mode == modeReview && e != nil {
		e.input.InsertString(text)
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
	// With no files to revert there is one question, not two.
	if b := m.undo.target; b != nil && !b.files {
		switch msg.String() {
		case "y", "Y", "enter":
			return m.confirmUndo(false)
		case "n", "N":
			return m.cancelUndo()
		}
	}
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

// forgetKey answers the delete question. Every key but y cancels.
func (m Model) forgetKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
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

// boundKey answers the step bound. The engine is paused, waiting.
func (m Model) boundKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.settling() {
		return m, nil
	}
	switch msg.String() {
	case "y", "Y", "enter":
		return m.answerBound(true)
	// Not esc: stopping a request takes a deliberate key, never a stray one.
	case "n", "N":
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
		return m, m.send(event.CancelCommand{})
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
	_, room := m.confirmLines()
	switch msg.String() {
	case "y", "Y", "enter", "n", "N":
		// Too soon to be an answer: likely the next key of something typed.
		if m.settling() {
			return m, nil
		}
	}
	switch msg.String() {
	case "y", "Y", "enter":
		if !m.confirmReady() {
			m.noteErr("read to the end of the command first: ↓ scrolls it")
			return m, nil
		}
		return m.approve()
	case "n", "N":
		return m.decline()
	case "down":
		m.scrollConfirm(1)
	case "up":
		m.scrollConfirm(-1)
	case "pgdown":
		m.scrollConfirm(room)
	case "pgup":
		m.scrollConfirm(-room)
	}
	return m, nil
}

// inputKey gives a focused idle input every keystroke: typing must
// never trigger navigation. Only pgup/pgdn and enter bypass it.
func (m Model) inputKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if next, cmd, handled := m.slashKey(msg); handled {
		return next, cmd
	}
	switch msg.String() {
	case "enter":
		if m.prompt.shell {
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

// slashKey drives the open dropdown's arrows and enter. Reports
// handled=false when no dropdown is open.
func (m Model) slashKey(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
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
	if msg.String() == "a" && len(m.agentOrder) > 0 {
		return m.showAgents("")
	}
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

func (m Model) historyKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if msg.String() == "a" && len(m.agentOrder) > 0 {
		return m.showAgents("")
	}
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
	case "end":
		m.followNewest()
		return m, nil
	case "pgup", "pgdown":
		return m.scrollViewport(msg.String())
	case "enter":
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
	case "v", "space":
		if r := m.focused(); r != nil {
			m.toggleExpand(r)
		}
		return m, nil
	case "x":
		if r := m.focused(); r != nil {
			return m.stopAgent(r.agent)
		}
	}
	return m, nil
}
