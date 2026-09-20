package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// runRollback parses "/rollback <step>" and, if a target goal exists,
// dispatches the restore off the update loop.
func (m Model) runRollback(cmd string) (tea.Model, tea.Cmd) {
	if m.waiting {
		m.noteErr("rollback: busy, try again once the current step finishes")
		return m, nil
	}
	fields := strings.Fields(cmd)
	step, err := strconv.Atoi(fields[len(fields)-1])
	if len(fields) != 2 || err != nil || step < 1 {
		m.noteErr("usage: /rollback <step> (a positive number, see the dim #N markers in history)")
		return m, nil
	}
	target := m.lastGoalBlock()
	if target == nil {
		m.noteErr("rollback: no goal to roll back")
		return m, nil
	}
	m.waiting = true
	return m, tea.Batch(m.spinner.Tick, rollbackCmd(m.ctx, m.sess, target, step))
}

// lastGoalBlock is what /rollback targets. Tool invocations (/tree,
// /usage) always get their own block with res == nil (see openTool).
func (m Model) lastGoalBlock() *goalBlock {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].res != nil {
			return m.blocks[i]
		}
	}
	return nil
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
	// Mirrors the same truncation onto this parallel UI-side list.
	if msg.step <= len(msg.target.steps) {
		msg.target.steps = msg.target.steps[:msg.step-1]
	}
	// The workspace is a bind mount, not part of the snapshot, so say
	// plainly that the user's own files were not reverted.
	m.noteOK(fmt.Sprintf("undid step %d onward (workspace files unchanged)", msg.step))
	m.nav.cursor = len(m.rows()) - 1
	if m.showWelcome() {
		// Undoing every step hands the pane back to the welcome
		// screen, whose ticker stopped when the first row appeared.
		return m, welcomeTick()
	}
	return m, nil
}
