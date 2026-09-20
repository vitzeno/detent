package ui

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for rollback_flow.go: /rollback parsing/dispatch and the
// history truncation that follows a successful restore.

func TestUI_Rollback_NoArgsShowsUsage(t *testing.T) {
	m := testUIModel()
	nm, cmd := m.runRollback("/rollback")
	m = nm.(Model)
	assert.Nil(t, cmd)
	assert.Contains(t, m.notice.text, "usage:")
	assert.False(t, m.waiting)
}

func TestUI_Rollback_NonNumericArgShowsUsage(t *testing.T) {
	m := testUIModel()
	nm, cmd := m.runRollback("/rollback two")
	m = nm.(Model)
	assert.Nil(t, cmd)
	assert.Contains(t, m.notice.text, "usage:")
}

func TestUI_Rollback_BusyShowsNotice(t *testing.T) {
	m := busyUIModel()
	nm, cmd := m.runRollback("/rollback 1")
	m = nm.(Model)
	assert.Nil(t, cmd)
	assert.Contains(t, m.notice.text, "busy")
}

func TestUI_Rollback_NoGoalShowsNotice(t *testing.T) {
	m := testUIModel()
	nm, cmd := m.runRollback("/rollback 1")
	m = nm.(Model)
	assert.Nil(t, cmd)
	assert.Contains(t, m.notice.text, "no goal")
}

func TestUI_Rollback_DispatchesAndSetsWaiting(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{res: &GoalResult{}, steps: []*stepRow{{}, {}}}}

	nm, cmd := m.runRollback("/rollback 1")
	m = nm.(Model)
	assert.True(t, m.waiting)
	assert.NotNil(t, cmd)
}

func TestUI_Rollback_TargetsLastRealGoalBlock(t *testing.T) {
	m := testUIModel()
	first := &goalBlock{res: &GoalResult{}}
	tool := &goalBlock{tool: "tree"} // res == nil: never a target
	second := &goalBlock{res: &GoalResult{}}
	m.blocks = []*goalBlock{first, tool, second}

	assert.Same(t, second, m.lastGoalBlock())
}

func TestUI_OnRollbackDone_TruncatesSteps(t *testing.T) {
	m := testUIModel()
	target := &goalBlock{res: &GoalResult{}, steps: []*stepRow{{command: "one"}, {command: "two"}, {command: "three"}}}
	m.blocks = []*goalBlock{target}
	m.waiting = true

	nm, cmd := m.Update(rollbackDoneMsg{target: target, step: 2, ok: true})
	m = nm.(Model)
	assert.Nil(t, cmd)
	assert.False(t, m.waiting)
	require.Len(t, target.steps, 1, "step 2 and everything after it is undone")
	assert.Equal(t, "one", target.steps[0].command)
	assert.Contains(t, m.notice.text, "undid step 2")
}

func TestUI_OnRollbackDone_UndoingEverythingRestartsWelcome(t *testing.T) {
	m := testUIModel()
	target := &goalBlock{res: &GoalResult{}, steps: []*stepRow{{command: "one"}}}
	m.blocks = []*goalBlock{target}

	nm, cmd := m.Update(rollbackDoneMsg{target: target, step: 1, ok: true})
	m = nm.(Model)
	require.Empty(t, target.steps)
	require.True(t, m.showWelcome(), "the welcome pane comes back")
	assert.NotNil(t, cmd, "and its animation restarts rather than sitting frozen")
}

func TestUI_OnRollbackDone_ErrorLeavesStepsIntact(t *testing.T) {
	m := testUIModel()
	target := &goalBlock{res: &GoalResult{}, steps: []*stepRow{{command: "one"}}}

	nm, _ := m.Update(rollbackDoneMsg{target: target, step: 5, err: errors.New("step 5 out of range (1-1)")})
	m = nm.(Model)
	assert.Len(t, target.steps, 1, "an error must not truncate anything")
	assert.Contains(t, m.notice.text, "step 5 out of range")
}

func TestUI_OnRollbackDone_NotOKShowsNoSandboxNotice(t *testing.T) {
	m := testUIModel()
	target := &goalBlock{res: &GoalResult{}, steps: []*stepRow{{command: "one"}}}

	nm, _ := m.Update(rollbackDoneMsg{target: target, step: 1, ok: false})
	m = nm.(Model)
	assert.Len(t, target.steps, 1)
	assert.Contains(t, m.notice.text, "no sandbox")
}
