package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/usage"
)

// Tests for goal_flow.go: the goal lifecycle state machine (start,
// propose, approve/decline, abort, done).

func TestCompletionDisagreement(t *testing.T) {
	low := agent.PostJudgment{FromJudge: true, GoalAchieved: 0.2}
	b := &goalBlock{steps: []*stepRow{{cmd: cmdState{ec: &agent.ExecutedCommand{Post: &low}}}}}
	require.Contains(t, completionDisagreement(b), "0.20")

	high := agent.PostJudgment{FromJudge: true, GoalAchieved: 0.9}
	b2 := &goalBlock{steps: []*stepRow{{cmd: cmdState{ec: &agent.ExecutedCommand{Post: &high}}}}}
	require.Empty(t, completionDisagreement(b2))

	heur := agent.PostJudgment{GoalAchieved: -1}
	b3 := &goalBlock{steps: []*stepRow{{cmd: cmdState{ec: &agent.ExecutedCommand{Post: &heur}}}}}
	require.Empty(t, completionDisagreement(b3), "no opinion must not warn")
}

func TestUI_GoalSubmitMovesFocusToHistory(t *testing.T) {
	m := New(context.Background(), testSession(), "test-model", "")
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	m.input.SetValue("real goal here")
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.Len(t, m.blocks, 1)
	require.Equal(t, "real goal here", m.blocks[0].goal)
	require.Equal(t, focusHistory, m.nav.focus)
}

// TestUI_StartGoalUpdatesHistoryWindowImmediately locks a bug where
// submitting a goal appended the block and called trackNewest, but
// nothing refreshed nav.histWindow (the cache View() actually reads) —
// the new goal line stayed invisible until some later, unrelated event
// (a spinner tick, a step finishing) happened to trigger a refresh.
func TestUI_StartGoalUpdatesHistoryWindowImmediately(t *testing.T) {
	m := testUIModel()
	m.input.SetValue("brand new goal text")
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.Contains(t, strings.Join(m.nav.histWindow, "\n"), "brand new goal text",
		"the goal must appear in the cached history window immediately, not on some later refresh")
}

func TestUI_StartGoalIsCancellable(t *testing.T) {
	m := testUIModel()
	m.input.SetValue("some goal")
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.NotNil(t, m.abort, "pending propose must be abortable")
}

func TestUI_OnPropose_AutoRunsWhenNotDangerous(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", res: &agent.GoalResult{Goal: "g"}}}
	m.cur = m.blocks[0]

	nm, cmd := m.onPropose(proposeMsg{
		proposal: propose.Proposal{Command: "ls", Rationale: "list"},
		pre:      agent.PreJudgment{Dangerous: false},
	})
	m = nm.(Model)
	assert.NotEqual(t, modeConfirm, m.mode, "a non-dangerous command must never enter confirm mode")
	require.Len(t, m.cur.steps, 1, "it must have run directly, via approve()")
	assert.Equal(t, "ls", m.cur.steps[0].command)
	assert.NotNil(t, cmd, "must dispatch execCmd the same as pressing y would")
}

func TestUI_OnPropose_StillConfirmsWhenDangerous(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", res: &agent.GoalResult{Goal: "g"}}}
	m.cur = m.blocks[0]

	nm, _ := m.onPropose(proposeMsg{
		proposal: propose.Proposal{Command: "rm -rf /tmp/x", Rationale: "remove"},
		pre:      agent.PreJudgment{Dangerous: true, RiskNote: "recursive remove"},
	})
	m = nm.(Model)
	assert.Equal(t, modeConfirm, m.mode)
	assert.Empty(t, m.cur.steps, "must not run until the human presses y")
}

func TestUI_AbortedProposeClosesBlock(t *testing.T) {
	sess := testSession()
	sess.Stats = usage.New()
	m := New(context.Background(), sess, "test-model", "")
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()

	// Establish "goal begun, propose in flight" directly — BeginGoal
	// itself now runs off the update loop (see TestUI_BeginGoalRunsAsync),
	// so this targets onPropose's own cancellation handling.
	res, err := sess.BeginGoal(context.Background(), "some goal")
	require.NoError(t, err)
	m.blocks = []*goalBlock{{goal: "some goal", res: res}}
	m.cur = m.blocks[0]
	require.Len(t, m.blocks, 1)

	nm, _ := m.Update(proposeMsg{err: context.Canceled})
	m = nm.(Model)
	require.True(t, m.blocks[0].ended)
	require.Equal(t, agent.EndAborted, m.blocks[0].end)
	require.Equal(t, focusInput, m.nav.focus)
	// RecordAbort closes the transcript turn it opened in BeginGoal, so a
	// later goal doesn't see this one's request left dangling — and
	// finishes the Stats.Goal BeginGoal already started, fixing what used
	// to be an open-forever /usage entry for any goal aborted mid-propose.
	require.Len(t, sess.Transcript, 2, "aborted propose must close the goal turn it opened")
	require.Equal(t, "some goal", sess.Transcript[0].Content)
	require.Equal(t, "[goal ended by human: aborted]", sess.Transcript[1].Content)
	require.False(t, res.Stats.Ended.IsZero(), "RecordAbort must finish the Stats.Goal BeginGoal started")
}

// TestUI_BeginGoalRunsAsync covers onBeginGoal directly: BeginGoal now
// does real work (a Jev call plus possible shell execs for probe
// collection), so it must run off the update loop via beginGoalCmd
// rather than block startGoal, and its outcome must route the same way
// onPropose's does — success advances into propose, cancellation and
// other errors both close the block, but distinctly.
func TestUI_BeginGoalRunsAsync(t *testing.T) {
	t.Run("success attaches res and advances into propose", func(t *testing.T) {
		m := testUIModel()
		m.blocks = []*goalBlock{{goal: "g"}}
		m.cur = m.blocks[0]

		res := &agent.GoalResult{Goal: "g"}
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
		assert.Equal(t, agent.EndAborted, m.blocks[0].end)
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
