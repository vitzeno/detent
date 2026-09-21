package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// runRollback parses "/rollback <step>", resolves that session-wide
// step number to the goal that owns it, and dispatches the restore off
// the update loop.
func (m Model) runRollback(cmd string) (tea.Model, tea.Cmd) {
	if m.waiting {
		m.noteErr("rollback: busy, try again once the current step finishes")
		return m, nil
	}
	fields := strings.Fields(cmd)
	step, err := strconv.Atoi(fields[len(fields)-1])
	if len(fields) != 2 || err != nil || step < 1 {
		m.noteErr("usage: /rollback <step> — the number in a row's dim #N marker")
		return m, nil
	}
	target, local, ok := m.findStep(step)
	if !ok {
		m.noteErr(fmt.Sprintf("rollback: no step #%d — this session has %d", step, m.stepCount()))
		return m, nil
	}

	// Ask before writing to the human's own files. The container rolls
	// back either way; the workspace may hold edits detent never made,
	// so reverting it is a decision, not a side effect.
	files, err := m.sess.PlanRollback(m.ctx, target.res, local)
	if err != nil {
		m.noteErr("rollback: " + err.Error())
		return m, nil
	}
	if len(files) > 0 {
		m.rollback = rollbackState{target: target, local: local, step: step, files: files}
		m.mode = modeRollbackConfirm
		return m, nil
	}
	return m.dispatchRollback(target, local, step, false)
}

// confirmRollback reverts the human's files along with the container.
func (m Model) confirmRollback() (tea.Model, tea.Cmd) {
	m.mode = modeInput
	r := m.rollback
	m.rollback = rollbackState{}
	if r.target == nil {
		return m, nil
	}
	return m.dispatchRollback(r.target, r.local, r.step, true)
}

// declineRollback rolls the container back and leaves the files alone,
// which is the old behaviour and still the safe one.
func (m Model) declineRollback() (tea.Model, tea.Cmd) {
	m.mode = modeInput
	r := m.rollback
	m.rollback = rollbackState{}
	if r.target == nil {
		return m, nil
	}
	return m.dispatchRollback(r.target, r.local, r.step, false)
}

// cancelRollback backs out without touching anything.
func (m Model) cancelRollback() (tea.Model, tea.Cmd) {
	m.mode = modeInput
	m.rollback = rollbackState{}
	m.noteErr("rollback cancelled")
	return m, nil
}

func (m Model) dispatchRollback(target *goalBlock, local, step int, revertFiles bool) (tea.Model, tea.Cmd) {
	m.waiting = true
	return m, tea.Batch(m.spinner.Tick, rollbackCmd(m.ctx, m.sess, target, local, step, revertFiles))
}

// findStep resolves a session-wide step number to the goal that owns
// it and that step's position within it, which is what the harness's
// own Rollback takes. Two numbering schemes is what made /rollback
// undo a step nobody had pointed at: the marker counted within a goal
// while the argument was read against the last one.
func (m Model) findStep(step int) (target *goalBlock, local int, ok bool) {
	n := 0
	for _, b := range m.blocks {
		if b.res == nil {
			continue // a tool block ran no commands
		}
		for i := range b.steps {
			n++
			if n == step {
				return b, i + 1, true
			}
		}
	}
	return nil, 0, false
}

// stepCount is how many steps the session has run, for error text.
func (m Model) stepCount() int {
	n := 0
	for _, b := range m.blocks {
		if b.res != nil {
			n += len(b.steps)
		}
	}
	return n
}

func (m Model) onRollbackDone(msg rollbackDoneMsg) (tea.Model, tea.Cmd) {
	m.waiting = false
	if msg.err != nil {
		m.noteErr("rollback: " + msg.err.Error())
		return m, nil
	}
	if !msg.ok {
		m.noteErr("rollback: no sandbox wired for this session")
		return m, nil
	}
	m.truncateFrom(msg.target, msg.local)

	if msg.revertedFiles {
		m.noteOK(fmt.Sprintf("undid #%d onward, your files with it", msg.step))
	} else {
		m.noteOK(fmt.Sprintf("undid #%d onward — files in the workspace are untouched", msg.step))
	}
	m.nav.follow = true
	m.nav.cursor = len(m.rows()) - 1
	if m.showWelcome() {
		// Undoing every step hands the pane back to the welcome
		// screen, whose ticker stopped when the first row appeared.
		return m, welcomeTick()
	}
	return m, nil
}

// truncateFrom drops the rolled-back step, the rest of its goal, and
// every goal after it — matching what the harness did to the
// transcript, which is session-wide. A goal left with no steps goes
// too: leaving its text and summary on screen made a rollback that had
// worked look like it had done nothing.
func (m *Model) truncateFrom(target *goalBlock, local int) {
	kept := make([]*goalBlock, 0, len(m.blocks))
	for _, b := range m.blocks {
		if b != target {
			kept = append(kept, b)
			continue
		}
		if local <= len(b.steps) {
			b.steps = b.steps[:local-1]
		}
		if len(b.steps) > 0 {
			// Open again: whatever it concluded rested on steps that
			// no longer exist.
			b.ended, b.summary, b.judge = false, "", goalJudgement{}
			kept = append(kept, b)
		}
		break // everything past the target goes with it
	}
	m.blocks = kept
	m.cur = nil
	m.totalCmds = m.stepCount()
}
