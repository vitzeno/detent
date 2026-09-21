package ui

import (
	"errors"
	"fmt"
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

func TestUI_Rollback_UnknownStepSaysWhatExists(t *testing.T) {
	m := testUIModel()
	nm, cmd := m.runRollback("/rollback 1")
	m = nm.(Model)
	assert.Nil(t, cmd)
	assert.Contains(t, m.notice.text, "no step #1")
	assert.Contains(t, m.notice.text, "has 0")

	m.blocks = []*goalBlock{{res: &GoalResult{}, steps: []*stepRow{{}, {}}}}
	nm, _ = m.runRollback("/rollback 9")
	assert.Contains(t, nm.(Model).notice.text, "this session has 2")
}

func TestUI_Rollback_DispatchesAndSetsWaiting(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{res: &GoalResult{}, steps: []*stepRow{{}, {}}}}

	nm, cmd := m.runRollback("/rollback 1")
	m = nm.(Model)
	assert.True(t, m.waiting)
	assert.NotNil(t, cmd)
}

// The dim #N marker counts across the session, so /rollback N has to
// resolve to the goal that owns step N — not, as it used to, step N of
// whichever goal happened to be last. With five goals of one command
// each every row read "#1" and the number addressed a different step
// from the one being pointed at.
func TestUI_Rollback_ResolvesSessionWideStepNumbers(t *testing.T) {
	m := testUIModel()
	first := &goalBlock{goal: "first", res: &GoalResult{}, steps: []*stepRow{{command: "a"}, {command: "b"}}}
	tool := &goalBlock{tool: "tree", steps: []*stepRow{{toolKind: "tree"}}} // res == nil: ran nothing
	second := &goalBlock{goal: "second", res: &GoalResult{}, steps: []*stepRow{{command: "c"}}}
	m.blocks = []*goalBlock{first, tool, second}

	for _, tc := range []struct {
		step  int
		block *goalBlock
		local int
	}{
		{1, first, 1},
		{2, first, 2},
		{3, second, 1}, // the tool block contributes no steps
	} {
		got, local, ok := m.findStep(tc.step)
		require.True(t, ok, "#%d must resolve", tc.step)
		assert.Same(t, tc.block, got, "#%d belongs to %q", tc.step, tc.block.goal)
		assert.Equal(t, tc.local, local, "#%d is that goal's step %d", tc.step, tc.local)
	}
	_, _, ok := m.findStep(4)
	assert.False(t, ok, "past the end resolves to nothing")
}

func TestUI_OnRollbackDone_TruncatesSteps(t *testing.T) {
	m := testUIModel()
	target := &goalBlock{res: &GoalResult{}, steps: []*stepRow{{command: "one"}, {command: "two"}, {command: "three"}}}
	m.blocks = []*goalBlock{target}
	m.waiting = true

	nm, cmd := m.Update(rollbackDoneMsg{target: target, local: 2, step: 2, ok: true})
	m = nm.(Model)
	assert.Nil(t, cmd)
	assert.False(t, m.waiting)
	require.Len(t, target.steps, 1, "step 2 and everything after it is undone")
	assert.Equal(t, "one", target.steps[0].command)
	assert.Contains(t, m.notice.text, "undid #2")
	assert.Contains(t, m.notice.text, "workspace", "the bind mount caveat has to be said")
}

// Undoing a step in an earlier goal takes every goal after it too —
// the harness truncates the transcript session-wide, and leaving the
// later blocks on screen is what made a working rollback look like it
// had done nothing.
func TestUI_OnRollbackDone_DropsEveryGoalAfterTheTarget(t *testing.T) {
	m := testUIModel()
	mk := func(goal string, n int) *goalBlock {
		b := &goalBlock{goal: goal, res: &GoalResult{Goal: goal}, ended: true, end: EndDone,
			summary: "did " + goal, judge: goalJudgement{scored: true, score: 0.9}}
		for i := range n {
			b.steps = append(b.steps, &stepRow{command: fmt.Sprintf("%s-%d", goal, i+1)})
		}
		return b
	}
	first, second, third := mk("first", 2), mk("second", 1), mk("third", 1)
	m.blocks = []*goalBlock{first, second, third}
	m.totalCmds = 4

	// Undo #2, which is the second step of the first goal.
	target, local, ok := m.findStep(2)
	require.True(t, ok)
	require.Same(t, first, target)
	nm, _ := m.Update(rollbackDoneMsg{target: target, local: local, step: 2, ok: true})
	m = nm.(Model)

	require.Len(t, m.blocks, 1, "the goals that followed go with it")
	assert.Same(t, first, m.blocks[0])
	assert.Len(t, first.steps, 1)
	assert.Equal(t, 1, m.totalCmds, "the session counter follows")

	// The target is open again: its summary rested on a step that is gone.
	assert.False(t, first.ended)
	assert.Empty(t, first.summary)
	assert.False(t, first.judge.scored)
}

// A goal left with no steps disappears rather than keeping its text
// and verdict on screen.
func TestUI_OnRollbackDone_EmptiedGoalIsRemoved(t *testing.T) {
	m := testUIModel()
	first := &goalBlock{goal: "first", res: &GoalResult{}, ended: true, end: EndDone,
		summary: "did it", steps: []*stepRow{{command: "a"}}}
	second := &goalBlock{goal: "second", res: &GoalResult{}, steps: []*stepRow{{command: "b"}}}
	m.blocks = []*goalBlock{first, second}

	nm, _ := m.Update(rollbackDoneMsg{target: first, local: 1, step: 1, ok: true})
	m = nm.(Model)
	assert.Empty(t, m.blocks, "nothing is left to show")
	assert.True(t, m.showWelcome())
}

func TestUI_OnRollbackDone_UndoingEverythingRestartsWelcome(t *testing.T) {
	m := testUIModel()
	target := &goalBlock{res: &GoalResult{}, steps: []*stepRow{{command: "one"}}}
	m.blocks = []*goalBlock{target}

	nm, cmd := m.Update(rollbackDoneMsg{target: target, local: 1, step: 1, ok: true})
	m = nm.(Model)
	require.Empty(t, target.steps)
	require.True(t, m.showWelcome(), "the welcome pane comes back")
	assert.NotNil(t, cmd, "and its animation restarts rather than sitting frozen")
}

func TestUI_OnRollbackDone_ErrorLeavesStepsIntact(t *testing.T) {
	m := testUIModel()
	target := &goalBlock{res: &GoalResult{}, steps: []*stepRow{{command: "one"}}}

	nm, _ := m.Update(rollbackDoneMsg{target: target, local: 5, step: 5, err: errors.New("step 5 out of range (1-1)")})
	m = nm.(Model)
	assert.Len(t, target.steps, 1, "an error must not truncate anything")
	assert.Contains(t, m.notice.text, "step 5 out of range")
}

func TestUI_OnRollbackDone_NotOKShowsNoSandboxNotice(t *testing.T) {
	m := testUIModel()
	target := &goalBlock{res: &GoalResult{}, steps: []*stepRow{{command: "one"}}}

	nm, _ := m.Update(rollbackDoneMsg{target: target, local: 1, step: 1, ok: false})
	m = nm.(Model)
	assert.Len(t, target.steps, 1)
	assert.Contains(t, m.notice.text, "no sandbox")
}
