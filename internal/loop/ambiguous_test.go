package loop

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capabilities"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/extract"
)

func TestRun_Prepare_Ambiguous_CarriesCandidatesAndProbabilities(t *testing.T) {
	candidates := []extract.Candidate{
		{ID: "c1", Desc: "drafts/report-v3.md", Fields: map[string]any{"path": "notes.txt"}},
	}
	judge := &scriptedJudge{t: t, responses: []classify.Answers{{
		"next_action":            choiceAnswer("test__with_path", 0.95),
		"goal_achieved":          noulAnswer(0.1),
		"goal_satisfiable":       noulAnswer(0.9),
		"path_target":            classify.Answer{Choice: "c1", Probabilities: map[string]float64{"c1": 0.6, "no_match": 0.4}},
		"path_target_resolvable": noulAnswer(0.2), // below TargetResolvableFloor
	}}}
	l, filePath, _ := newTestLoop(t, judge, nil, DefaultBudgets)
	candidates[0].Fields["path"] = filePath
	l.Constructor = fakeConstructor(func(argType capabilities.ArgType, _ string, _ extract.State) ([]extract.Candidate, error) {
		if argType != capabilities.ArgPath {
			return nil, nil
		}
		return candidates, nil
	})

	run := l.NewRun("read the file")
	prepared, term, err := run.Prepare(context.Background())
	require.NoError(t, err)
	require.Nil(t, term)
	require.NotNil(t, prepared.Ambiguous)
	assert.Equal(t, capabilities.ArgPath, prepared.Ambiguous.ArgType)
	assert.Equal(t, "path", prepared.Ambiguous.ArgName)
	assert.Equal(t, 0.2, prepared.Ambiguous.Noul)
	require.Len(t, prepared.Ambiguous.Candidates, 1)
	assert.Equal(t, 0.6, prepared.Ambiguous.Probabilities["c1"])
	assert.Empty(t, run.State().Resolved, "nothing decided yet — no human pick made")
}

func TestRun_ResolveAmbiguous_ThenCommit_SafeCapability(t *testing.T) {
	judge := &scriptedJudge{t: t, responses: []classify.Answers{
		{
			"next_action":            choiceAnswer("test__with_path", 0.95),
			"goal_achieved":          noulAnswer(0.1),
			"goal_satisfiable":       noulAnswer(0.9),
			"path_target":            classify.Answer{Choice: "c1", Probabilities: map[string]float64{"c1": 0.55, "no_match": 0.45}},
			"path_target_resolvable": noulAnswer(0.3),
		},
	}}
	l, filePath, _ := newTestLoop(t, judge, nil, DefaultBudgets)
	l.Constructor = fakeConstructor(func(argType capabilities.ArgType, _ string, _ extract.State) ([]extract.Candidate, error) {
		if argType != capabilities.ArgPath {
			return nil, nil
		}
		return []extract.Candidate{{ID: "c1", Desc: filePath, Fields: map[string]any{"path": filePath}}}, nil
	})

	run := l.NewRun("read the file")
	ctx := context.Background()
	prepared, term, err := run.Prepare(ctx)
	require.NoError(t, err)
	require.Nil(t, term)
	require.NotNil(t, prepared.Ambiguous)

	resolved, err := run.ResolveAmbiguous(prepared, "c1")
	require.NoError(t, err)
	assert.Nil(t, resolved.Confirm, "test__with_path is safe — no confirm needed even after resolving")
	// gate.ValidatePath resolves symlinks (macOS: /var is itself a symlink
	// to /private/var) — compare against the same resolution.
	wantPath, err := filepath.EvalSymlinks(filePath)
	require.NoError(t, err)
	assert.Equal(t, wantPath, resolved.Args["path"])

	require.Len(t, run.State().Resolved, 1)
	assert.Equal(t, "path", run.State().Resolved[0].Arg)
	assert.Equal(t, 0.3, run.State().Resolved[0].TargetResolvableNoul)

	finding, err := run.Commit(ctx, resolved)
	require.NoError(t, err)
	assert.Equal(t, "test__with_path", finding.Action)
}

func TestRun_ResolveAmbiguous_MutationStillNeedsConfirm(t *testing.T) {
	// Real process data first (step 1: list_procs, via the real
	// DeterministicConstructor + process_lines reducer, same as
	// TestLoop_Run_KillLikeFlow_ResolvesPIDViaRealProcessListStep) — a
	// literal, unverified pid can never reach ResolveAmbiguous's gate
	// check successfully, so this test needs the same real setup.
	judge := &scriptedJudge{t: t, responses: []classify.Answers{
		{
			"next_action":      choiceAnswer("test__list_procs", 0.95),
			"goal_achieved":    noulAnswer(0.1),
			"goal_satisfiable": noulAnswer(0.9),
		},
		{
			"next_action":           choiceAnswer("test__mutate_with_pid", 0.95),
			"goal_achieved":         noulAnswer(0.1),
			"goal_satisfiable":      noulAnswer(0.9),
			"pid_target":            classify.Answer{Choice: "pid_4821", Probabilities: map[string]float64{"pid_4821": 0.5, "pid_5140": 0.4, "no_match": 0.1}},
			"pid_target_resolvable": noulAnswer(0.1),
		},
	}}
	l, _, counters := newTestLoop(t, judge, nil, DefaultBudgets)
	l.Constructor = &extract.DeterministicConstructor{}

	run := l.NewRun("kill the node process")
	ctx := context.Background()

	step1, term, err := run.Prepare(ctx)
	require.NoError(t, err)
	require.Nil(t, term)
	_, err = run.Commit(ctx, step1)
	require.NoError(t, err)

	prepared, term, err := run.Prepare(ctx)
	require.NoError(t, err)
	require.Nil(t, term)
	require.NotNil(t, prepared.Ambiguous)

	resolved, err := run.ResolveAmbiguous(prepared, "pid_4821")
	require.NoError(t, err)
	require.NotNil(t, resolved.Confirm, "mutation still needs approval even after the target is resolved")
	assert.Equal(t, "test__mutate_with_pid", resolved.Confirm.Capability)
	assert.Equal(t, 0, counters.MutateWithPIDCalls, "resolving alone must never dispatch")

	_, err = run.Commit(ctx, resolved)
	require.NoError(t, err)
	assert.Equal(t, 1, counters.MutateWithPIDCalls)
}

func TestRun_ResolveAmbiguous_UnknownCandidateID_Errors(t *testing.T) {
	judge := &scriptedJudge{t: t, responses: []classify.Answers{{
		"next_action":            choiceAnswer("test__with_path", 0.95),
		"goal_achieved":          noulAnswer(0.1),
		"goal_satisfiable":       noulAnswer(0.9),
		"path_target":            classify.Answer{Choice: "c1", Probabilities: map[string]float64{"c1": 0.5, "no_match": 0.5}},
		"path_target_resolvable": noulAnswer(0.1),
	}}}
	l, filePath, _ := newTestLoop(t, judge, nil, DefaultBudgets)
	l.Constructor = fakeConstructor(func(argType capabilities.ArgType, _ string, _ extract.State) ([]extract.Candidate, error) {
		if argType != capabilities.ArgPath {
			return nil, nil
		}
		return []extract.Candidate{{ID: "c1", Desc: filePath, Fields: map[string]any{"path": filePath}}}, nil
	})

	run := l.NewRun("read the file")
	prepared, _, err := run.Prepare(context.Background())
	require.NoError(t, err)

	_, err = run.ResolveAmbiguous(prepared, "does-not-exist")
	assert.Error(t, err)
}

// TestLoop_Run_StillTerminatesOnAmbiguous_NoHumanToAsk proves the
// synchronous convenience path's existing CLI-facing behavior is
// unchanged by this refactor — it has no human to ask, so an ambiguous
// target still ends the run, exactly as before.
func TestLoop_Run_StillTerminatesOnAmbiguous_NoHumanToAsk(t *testing.T) {
	judge := &scriptedJudge{t: t, responses: []classify.Answers{{
		"next_action":            choiceAnswer("test__with_path", 0.95),
		"goal_achieved":          noulAnswer(0.1),
		"goal_satisfiable":       noulAnswer(0.9),
		"path_target":            classify.Answer{Choice: "c1", Probabilities: map[string]float64{"c1": 0.5, "no_match": 0.5}},
		"path_target_resolvable": noulAnswer(0.1),
	}}}
	l, filePath, counters := newTestLoop(t, judge, nil, DefaultBudgets)
	l.Constructor = fakeConstructor(func(argType capabilities.ArgType, _ string, _ extract.State) ([]extract.Candidate, error) {
		if argType != capabilities.ArgPath {
			return nil, nil
		}
		return []extract.Candidate{{ID: "c1", Desc: filePath, Fields: map[string]any{"path": filePath}}}, nil
	})

	state, term, err := l.Run(context.Background(), "read the file")
	require.NoError(t, err)
	assert.Equal(t, ReasonAmbiguousTarget, term.Reason)
	assert.Contains(t, term.Detail, "path_target_resolvable")
	assert.Equal(t, 0, counters.MutateWithPIDCalls)
	assert.Empty(t, state.Findings)
}
