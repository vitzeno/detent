package loop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/classify"
)

// These exercise NewRun/Prepare/Commit/Decline directly — the primitives
// a step-driven caller (the TUI) uses instead of the blocking Loop.Run
// convenience wrapper, which is itself now built on these same calls.

func TestRun_Prepare_SafeCapability_NoConfirmNeeded(t *testing.T) {
	judge := &scriptedJudge{t: t, responses: []classify.Answers{{
		"next_action":      choiceAnswer("test__no_args", 0.95),
		"goal_achieved":    noulAnswer(0.1),
		"goal_satisfiable": noulAnswer(0.9),
	}}}
	l, _, counters := newTestLoop(t, judge, fakeConstructor(noCandidates), DefaultBudgets)

	run := l.NewRun("do something safe")
	prepared, term, err := run.Prepare(context.Background())
	require.NoError(t, err)
	require.Nil(t, term)
	require.NotNil(t, prepared)
	assert.Equal(t, "test__no_args", prepared.Capability)
	assert.Nil(t, prepared.Confirm, "safe capability must not require confirm")

	finding, err := run.Commit(context.Background(), prepared)
	require.NoError(t, err)
	assert.Equal(t, "test__no_args", finding.Action)
	assert.Equal(t, 0, counters.MutateCalls)
}

func TestRun_Prepare_Mutation_CarriesConfirmRequest(t *testing.T) {
	judge := &scriptedJudge{t: t, responses: []classify.Answers{{
		"next_action":      choiceAnswer("test__mutate", 0.95),
		"goal_achieved":    noulAnswer(0.1),
		"goal_satisfiable": noulAnswer(0.9),
	}}}
	l, _, counters := newTestLoop(t, judge, fakeConstructor(noCandidates), DefaultBudgets)

	run := l.NewRun("mutate something")
	prepared, term, err := run.Prepare(context.Background())
	require.NoError(t, err)
	require.Nil(t, term)
	require.NotNil(t, prepared.Confirm)
	assert.Equal(t, "test__mutate", prepared.Confirm.Capability)
	assert.Equal(t, 0, prepared.Confirm.WritesUsed, "not yet spent — this would be the first")
	assert.Equal(t, 0, counters.MutateCalls, "Prepare alone must never execute")

	// Not committed yet — confirming is the caller's job now.
	assert.Equal(t, 0, counters.MutateCalls)

	finding, err := run.Commit(context.Background(), prepared)
	require.NoError(t, err)
	assert.Equal(t, "test__mutate", finding.Action)
	assert.Equal(t, 1, counters.MutateCalls)
}

func TestRun_Decline_StopsWithoutCommitting(t *testing.T) {
	judge := &scriptedJudge{t: t, responses: []classify.Answers{{
		"next_action":      choiceAnswer("test__mutate", 0.95),
		"goal_achieved":    noulAnswer(0.1),
		"goal_satisfiable": noulAnswer(0.9),
	}}}
	l, _, counters := newTestLoop(t, judge, fakeConstructor(noCandidates), DefaultBudgets)

	run := l.NewRun("mutate something")
	prepared, term, err := run.Prepare(context.Background())
	require.NoError(t, err)
	require.Nil(t, term)
	require.NotNil(t, prepared.Confirm)

	got := run.Decline(prepared.Capability)
	assert.Equal(t, ReasonDeclined, got.Reason)
	assert.Equal(t, 0, counters.MutateCalls, "declining must never call Commit")
	assert.Empty(t, run.State().Findings)
}

func TestRun_MultipleSteps_StateAccumulatesAcrossCalls(t *testing.T) {
	judge := &scriptedJudge{t: t, responses: []classify.Answers{
		{
			"next_action":      choiceAnswer("test__no_args", 0.95),
			"goal_achieved":    noulAnswer(0.1),
			"goal_satisfiable": noulAnswer(0.9),
		},
		{
			"next_action":      choiceAnswer("done", 0.95),
			"goal_achieved":    noulAnswer(0.99),
			"goal_satisfiable": noulAnswer(0.9),
		},
	}}
	l, _, _ := newTestLoop(t, judge, fakeConstructor(noCandidates), DefaultBudgets)

	run := l.NewRun("do the thing")
	ctx := context.Background()

	prepared, term, err := run.Prepare(ctx)
	require.NoError(t, err)
	require.Nil(t, term)
	_, err = run.Commit(ctx, prepared)
	require.NoError(t, err)
	assert.Len(t, run.State().Findings, 1)

	_, term, err = run.Prepare(ctx)
	require.NoError(t, err)
	require.NotNil(t, term)
	assert.Equal(t, ReasonGoalAchieved, term.Reason)
	assert.Len(t, run.State().Findings, 1, "state from step 1 must still be there")
}

// TestLoop_Run_MatchesStepDrivenEquivalent proves the blocking
// convenience wrapper and the step-driven primitives agree — Loop.Run is
// now built on NewRun/Prepare/Commit, not a separate implementation that
// could quietly drift from what the TUI actually exercises.
func TestLoop_Run_MatchesStepDrivenEquivalent(t *testing.T) {
	responses := []classify.Answers{
		{
			"next_action":      choiceAnswer("test__mutate", 0.95),
			"goal_achieved":    noulAnswer(0.1),
			"goal_satisfiable": noulAnswer(0.9),
		},
		{
			"next_action":      choiceAnswer("done", 0.95),
			"goal_achieved":    noulAnswer(0.99),
			"goal_satisfiable": noulAnswer(0.9),
		},
	}

	judge := &scriptedJudge{t: t, responses: responses}
	l, _, _ := newTestLoop(t, judge, fakeConstructor(noCandidates), DefaultBudgets)
	l.Confirm = alwaysApprove

	state, term, err := l.Run(context.Background(), "mutate then finish")
	require.NoError(t, err)
	assert.Equal(t, ReasonGoalAchieved, term.Reason)
	require.Len(t, state.Findings, 1)
	assert.Equal(t, "test__mutate", state.Findings[0].Action)
}
