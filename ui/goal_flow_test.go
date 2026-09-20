package ui

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for goal_flow.go: the goal lifecycle state machine (start,
// propose, approve/decline, abort, done).

func TestCompletionDisagreement(t *testing.T) {
	low := PostJudgment{FromJudge: true, GoalAchieved: 0.2}
	b := &goalBlock{steps: []*stepRow{{cmd: cmdState{ec: &ExecutedCommand{Post: &low}}}}}
	require.Contains(t, completionDisagreement(b), "0.20")

	high := PostJudgment{FromJudge: true, GoalAchieved: 0.9}
	b2 := &goalBlock{steps: []*stepRow{{cmd: cmdState{ec: &ExecutedCommand{Post: &high}}}}}
	require.Empty(t, completionDisagreement(b2))

	heur := PostJudgment{GoalAchieved: -1}
	b3 := &goalBlock{steps: []*stepRow{{cmd: cmdState{ec: &ExecutedCommand{Post: &heur}}}}}
	require.Empty(t, completionDisagreement(b3), "no opinion must not warn")
}

func TestUI_GoalSubmitMovesFocusToHistory(t *testing.T) {
	m := New(context.Background(), newFakeDriver(), SessionInfo{Proposer: "test-model"})
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	m.prompt.SetValue("real goal here")
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.Len(t, m.blocks, 1)
	require.Equal(t, "real goal here", m.blocks[0].goal)
	require.Equal(t, focusHistory, m.nav.focus)
}

// TestUI_StartGoalUpdatesHistoryWindowImmediately locks a bug where
// the history pane never refreshed on submit, so the new goal line
// stayed invisible until something else happened to trigger one.
// Driven through Update, the way Bubble Tea does it.
func TestUI_StartGoalUpdatesHistoryWindowImmediately(t *testing.T) {
	m := testUIModel()
	m.prompt.SetValue("brand new goal text")
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.Contains(t, m.View(), "brand new goal text",
		"the goal must show the moment it's submitted, not on some later refresh")
}

func TestUI_StartGoalIsCancellable(t *testing.T) {
	m := testUIModel()
	m.prompt.SetValue("some goal")
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.NotNil(t, m.abort, "pending propose must be abortable")
}

func TestUI_OnPropose_AutoRunsWhenNotDangerous(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", res: &GoalResult{Goal: "g"}}}
	m.cur = m.blocks[0]

	nm, cmd := m.onPropose(proposeMsg{
		proposal: Proposal{Command: "ls", Rationale: "list"},
		pre:      PreJudgment{Dangerous: false},
	})
	m = nm.(Model)
	assert.NotEqual(t, modeConfirm, m.mode, "a non-dangerous command must never enter confirm mode")
	require.Len(t, m.cur.steps, 1, "it must have run directly, via approve()")
	assert.Equal(t, "ls", m.cur.steps[0].command)
	assert.NotNil(t, cmd, "must dispatch execCmd the same as pressing y would")
}

func TestUI_OnPropose_StillConfirmsWhenDangerous(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", res: &GoalResult{Goal: "g"}}}
	m.cur = m.blocks[0]

	nm, _ := m.onPropose(proposeMsg{
		proposal: Proposal{Command: "rm -rf /tmp/x", Rationale: "remove"},
		pre:      PreJudgment{Dangerous: true, RiskNote: "recursive remove"},
	})
	m = nm.(Model)
	assert.Equal(t, modeConfirm, m.mode)
	assert.Empty(t, m.cur.steps, "must not run until the human presses y")
}

// TestUI_AbortedProposeClosesBlock covers onPropose's cancellation
// routing: a canceled propose calls RecordAbort and closes the block
// as EndAborted, back to input focus. RecordAbort's own contract is
// agent's to keep — see TestSession_RecordAbort_ClosesTranscriptAndStats.
func TestUI_AbortedProposeClosesBlock(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "some goal", res: &GoalResult{Goal: "some goal"}}}
	m.cur = m.blocks[0]

	nm, _ := m.Update(proposeMsg{err: context.Canceled})
	m = nm.(Model)
	require.True(t, m.blocks[0].ended)
	require.Equal(t, EndAborted, m.blocks[0].end)
	require.Equal(t, focusInput, m.nav.focus)
}

// TestUI_BeginGoalRunsAsync covers onBeginGoal directly: success
// advances into propose, cancellation and other errors both close the
// block, but distinctly.
func TestUI_BeginGoalRunsAsync(t *testing.T) {
	t.Run("success attaches res and advances into propose", func(t *testing.T) {
		m := testUIModel()
		m.blocks = []*goalBlock{{goal: "g"}}
		m.cur = m.blocks[0]

		res := &GoalResult{Goal: "g"}
		nm, cmd := m.onBeginGoal(beginGoalMsg{goal: "g", res: res})
		m = nm.(Model)
		require.NotNil(t, cmd, "must dispatch the first proposeCmd")
		assert.Same(t, res, m.cur.res)
		assert.True(t, m.waiting)
		assert.NotNil(t, m.abort)
	})

	t.Run("cancellation closes the block as aborted", func(t *testing.T) {
		m := testUIModel()
		m.blocks = []*goalBlock{{goal: "g"}}
		m.cur = m.blocks[0]

		nm, cmd := m.onBeginGoal(beginGoalMsg{goal: "g", err: context.Canceled})
		m = nm.(Model)
		assert.Nil(t, cmd)
		require.True(t, m.blocks[0].ended)
		assert.Equal(t, EndAborted, m.blocks[0].end)
		assert.Nil(t, m.cur)
		assert.Equal(t, focusInput, m.nav.focus)
	})

	t.Run("other error closes the block with fatalErr, not EndAborted", func(t *testing.T) {
		m := testUIModel()
		m.blocks = []*goalBlock{{goal: "g"}}
		m.cur = m.blocks[0]

		wantErr := errors.New("no proposer wired")
		nm, cmd := m.onBeginGoal(beginGoalMsg{goal: "g", err: wantErr})
		m = nm.(Model)
		assert.Nil(t, cmd)
		require.True(t, m.blocks[0].ended)
		assert.Equal(t, wantErr, m.blocks[0].fatalErr)
		assert.Empty(t, m.blocks[0].end, "not the abort path")
		assert.Nil(t, m.cur)
	})
}
