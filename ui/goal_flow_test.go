package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for goal_flow.go: the goal lifecycle state machine (start,
// propose, approve/decline, abort, done).

// Jev reports on every goal, not only the ones it disagrees with.
// Silence used to cover both "it agrees" and "nothing judged this",
// which are the two readings a second opinion exists to tell apart.
func TestGoalVerdict_ReportsEveryJudgedGoal(t *testing.T) {
	judged := func(score float64, fromJudge bool) *goalBlock {
		p := PostJudgment{FromJudge: fromJudge, GoalAchieved: score}
		return &goalBlock{steps: []*stepRow{{cmd: cmdState{ec: &ExecutedCommand{Post: &p}}}}}
	}
	// Only the verdicts worth acting on carry a mark: agreement is the
	// expected case, and flagging it crowds out the two that aren't.
	for _, tc := range []struct {
		name  string
		score float64
		want  string
		mark  string
	}{
		{"met", 0.92, "goal met", ""},
		{"partly met", 0.62, "only partly met", "~"},
		{"unmet", 0.20, "looks unmet", "⚠"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := judged(tc.score, true)
			b.judge = goalVerdict(b)
			require.True(t, b.judge.scored)

			m := testUIModel()
			got := plain(strings.Join(m.judgeLines(b, 60), " "))
			assert.Contains(t, got, tc.want)
			assert.Contains(t, got, fmt.Sprintf("%.2f", tc.score), "the score itself is shown")

			for _, glyph := range []string{"⚠", "~", "✔", "✓"} {
				if glyph == tc.mark {
					assert.Contains(t, got, glyph, "this verdict wants flagging")
					continue
				}
				assert.NotContains(t, got, glyph, "%q must not be marked with %q", got, glyph)
			}
		})
	}

	// No judge wired, or it declined: say nothing rather than imply a
	// low score.
	heur := judged(-1, false)
	heur.judge = goalVerdict(heur)
	assert.False(t, heur.judge.scored)
	assert.Empty(t, testUIModel().judgeLines(heur, 60))
}

// The verdict reaches the history pane on a finished goal.
func TestGoalVerdict_ShowsInTheHistoryBanner(t *testing.T) {
	m := testUIModel()
	p := PostJudgment{FromJudge: true, GoalAchieved: 0.91}
	b := &goalBlock{
		goal: "g", res: &GoalResult{Goal: "g"}, ended: true, end: EndDone, summary: "did it",
		steps: []*stepRow{
			{command: "ls", cmd: cmdState{ec: &ExecutedCommand{Command: "ls", Post: &p}}},
			{prose: "did it"},
		},
	}
	b.judge = goalVerdict(b)
	m.blocks = []*goalBlock{b}
	m.sizeViewport()

	v := plain(m.View().Content)
	assert.Contains(t, v, "did it", "the prose row")
	assert.Contains(t, v, "jev")
	assert.Contains(t, v, "0.91")
}

// Submitting a goal leaves the input focused, which is what keeps
// /abort typeable while the model is thinking. Blurring it sent
// owner() down the history branch and busyKey — written for exactly
// this — was never reached.
func TestUI_GoalSubmitKeepsTheInputUsable(t *testing.T) {
	m := testUIModel()
	m.prompt.SetValue("real goal here")
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)

	require.Len(t, m.blocks, 1)
	require.Equal(t, "real goal here", m.blocks[0].goal)
	require.True(t, m.waiting)
	assert.Equal(t, focusInput, m.nav.focus)
	assert.True(t, m.prompt.Focused())
	assert.Equal(t, ownerBusy, m.owner(), "busyKey owns the keyboard while a goal runs")
}

// While busy the input takes slash commands and nothing else: /abort
// has to work, starting a second goal must not.
func TestUI_AbortIsTypeableWhileThinking(t *testing.T) {
	m := testUIModel()
	m.prompt.SetValue("real goal here")
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)

	aborted := false
	m.abort = func() { aborted = true }

	for _, r := range "/abort" {
		m = typeRune(m, r)
	}
	require.Equal(t, "/abort", m.prompt.Value(), "slash entry reaches the box while busy")

	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	assert.True(t, aborted, "/abort must reach the cancel func")
	assert.Equal(t, "abort sent", m.notice.text)
	assert.Len(t, m.blocks, 1, "and must not open a second goal")
}

// A plain goal typed mid-run is ignored rather than queued or started.
func TestUI_PlainTypingIsIgnoredWhileThinking(t *testing.T) {
	m := testUIModel()
	m.prompt.SetValue("real goal here")
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)

	for _, r := range "another goal" {
		m = typeRune(m, r)
	}
	assert.Empty(t, m.prompt.Value(), "only / opens the box while a goal is running")
	assert.Len(t, m.blocks, 1)
}

// TestUI_StartGoalUpdatesHistoryWindowImmediately locks a bug where
// the history pane never refreshed on submit, so the new goal line
// stayed invisible until something else happened to trigger one.
// Driven through Update, the way Bubble Tea does it.
func TestUI_StartGoalUpdatesHistoryWindowImmediately(t *testing.T) {
	m := testUIModel()
	m.prompt.SetValue("brand new goal text")
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	require.Contains(t, m.View().Content, "brand new goal text",
		"the goal must show the moment it's submitted, not on some later refresh")
}

func TestUI_StartGoalIsCancellable(t *testing.T) {
	m := testUIModel()
	m.prompt.SetValue("some goal")
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
