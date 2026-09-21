package ui

import (
	tea "charm.land/bubbletea/v2"
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

// The reported failure end to end: a goal writes a file, the human
// rolls back, and nothing had touched the file. Rolling back now
// offers to revert it — and asks first, because the workspace can hold
// edits detent never made.
func TestUI_Rollback_AsksBeforeTouchingTheHumansFiles(t *testing.T) {
	setup := func() (Model, *fakeDriver, *goalBlock) {
		m := testUIModel()
		drv := m.sess.(*fakeDriver)
		drv.planFiles = []FileChange{
			{Path: "test.py", Removed: true},
			{Path: "notes.md"},
		}
		b := &goalBlock{goal: "write a script", res: &GoalResult{Goal: "g"}, ended: true, end: EndDone,
			steps: []*stepRow{{command: "cat > test.py"}}}
		m.blocks = []*goalBlock{b}
		m.sizeViewport()
		return m, drv, b
	}

	t.Run("names every file before writing any", func(t *testing.T) {
		m, drv, _ := setup()
		nm, cmd := m.runSlash("/rollback 1")
		m = nm.(Model)
		m.sizeViewport() // runSlash is called directly here; Update does this

		require.Equal(t, modeRollbackConfirm, m.mode, "it must ask, not act")
		assert.Nil(t, cmd, "and dispatch nothing until it has an answer")
		assert.Zero(t, drv.rolledBackN, "the harness has not been called")

		view := plain(m.View().Content)
		assert.Contains(t, view, "test.py")
		assert.Contains(t, view, "notes.md")
		assert.Contains(t, view, "delete", "a file created since the checkpoint is deleted")
		assert.Contains(t, view, "restore", "one that changed comes back")
	})

	t.Run("y reverts the files too", func(t *testing.T) {
		m, drv, _ := setup()
		nm, _ := m.runSlash("/rollback 1")
		nm, cmd := nm.(Model).Update(typeKey("y"))
		require.NotNil(t, cmd)
		_ = nm
		msg := rollbackCmd(m.ctx, m.sess, m.blocks[0], 1, 1, true)().(rollbackDoneMsg)
		assert.True(t, drv.rolledBackFiles, "the harness was asked for the files back")
		assert.True(t, msg.revertedFiles)
	})

	t.Run("n rolls the container back and leaves them", func(t *testing.T) {
		m, drv, _ := setup()
		nm, _ := m.runSlash("/rollback 1")
		nm, cmd := nm.(Model).Update(typeKey("n"))
		require.NotNil(t, cmd)
		assert.Equal(t, modeInput, nm.(Model).mode)
		_ = rollbackCmd(m.ctx, m.sess, m.blocks[0], 1, 1, false)()
		assert.False(t, drv.rolledBackFiles, "the files are left alone")
	})

	t.Run("esc does nothing at all", func(t *testing.T) {
		m, drv, b := setup()
		nm, _ := m.runSlash("/rollback 1")
		nm, cmd := nm.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		m = nm.(Model)

		assert.Nil(t, cmd)
		assert.Equal(t, modeInput, m.mode)
		assert.Zero(t, drv.rolledBackN, "nothing was rolled back")
		assert.Len(t, b.steps, 1, "and history is untouched")
		assert.Contains(t, m.notice.text, "cancelled")
	})
}

// Nothing to revert means nothing to ask about.
func TestUI_Rollback_SkipsTheConfirmWhenNoFilesChange(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{res: &GoalResult{}, steps: []*stepRow{{command: "ls"}}}}
	m.sizeViewport()

	nm, cmd := m.runSlash("/rollback 1")
	m = nm.(Model)
	assert.Equal(t, modeInput, m.mode, "no confirm when the workspace is unaffected")
	assert.NotNil(t, cmd, "it just goes")
	assert.True(t, m.waiting)
}

// A command still waiting on a human hasn't succeeded, so the status
// line must not flash it green before it has an answer.
func TestUI_Rollback_NoSuccessFlashWhileConfirming(t *testing.T) {
	m := testUIModel()
	m.sess.(*fakeDriver).planFiles = []FileChange{{Path: "test.py", Removed: true}}
	m.blocks = []*goalBlock{{res: &GoalResult{}, steps: []*stepRow{{command: "cat > test.py"}}}}
	m.sizeViewport()

	nm, _ := m.runSlash("/rollback 1")
	m = nm.(Model)
	require.Equal(t, modeRollbackConfirm, m.mode)
	assert.Empty(t, m.notice.text, "nothing to report until the human answers")
}

// Reverting is the one irreversible thing a rollback does, and the
// case that bites is a file the human edited themselves while detent
// sat idle — nothing it ran accounts for that, and reverting throws
// the work away. So those are marked, counted, and enter takes the
// safe branch rather than the destructive one.
func TestUI_Rollback_FlagsWorkDetentDidNotMake(t *testing.T) {
	m := testUIModel()
	m.sess.(*fakeDriver).planFiles = []FileChange{
		{Path: "internal/sandbox/container.go", Unseen: true},
		{Path: "test.py", Removed: true},
	}
	m.blocks = []*goalBlock{{goal: "write a script", res: &GoalResult{Goal: "g"}, ended: true,
		end: EndDone, steps: []*stepRow{{command: "cat > test.py"}}}}
	m.sizeViewport()

	nm, _ := m.runSlash("/rollback 1")
	m = nm.(Model)
	m.sizeViewport()
	require.Equal(t, modeRollbackConfirm, m.mode)

	view := plain(m.View().Content)
	assert.Contains(t, view, "not detent's", "the file it never touched is marked")
	assert.Contains(t, view, "1 file changed after detent's last step",
		"and counted, so the risk is stated once in plain words")
	assert.Contains(t, view, "would change 2 files")

	// enter is the safe answer: destroying work has to be typed.
	drv := m.sess.(*fakeDriver)
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.Equal(t, modeInput, nm.(Model).mode)
	_ = rollbackCmd(m.ctx, m.sess, m.blocks[0], 1, 1, false)()
	assert.False(t, drv.rolledBackFiles, "enter rolls back the container only")
}

// A wide-reaching goal touches far more files than a modal can hold,
// and a list you can't read to the end isn't a list you can approve.
// The output pane takes it, so the existing viewport scrolls it.
func TestUI_Rollback_FileListTakesThePaneAndScrolls(t *testing.T) {
	m := testUIModel()
	var files []FileChange
	for i := range 120 {
		files = append(files, FileChange{Path: fmt.Sprintf("internal/pkg%03d/file.go", i)})
	}
	m.sess.(*fakeDriver).planFiles = files
	m.blocks = []*goalBlock{{goal: "rewrite the layout", res: &GoalResult{Goal: "g"}, ended: true,
		end: EndDone, steps: []*stepRow{{command: "mv"}}}}
	m.sizeViewport()

	m.prompt.SetValue("/rollback 1")
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	require.Equal(t, modeRollbackConfirm, m.mode)

	assert.Contains(t, plain(m.View().Content), "reverting — 120 files",
		"the pane header says what it is showing")
	assert.Contains(t, plain(m.View().Content), "pkg000/file.go", "starting at the top")
	assert.NotContains(t, plain(m.View().Content), "+", "nothing is elided into a '+N more'")

	// Paging reaches entries a fixed-height box could never have shown.
	for range 6 {
		nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
		m = nm.(Model)
	}
	deep := plain(m.View().Content)
	assert.Contains(t, deep, "pkg07", "paging reaches the far end of the list")
	assert.NotContains(t, deep, "pkg000/file.go", "and leaves the top behind")

	// Scrolling must not be mistaken for answering.
	assert.Equal(t, modeRollbackConfirm, m.mode)
	assert.Zero(t, m.sess.(*fakeDriver).rolledBackN)
}
