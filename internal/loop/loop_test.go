package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capabilities"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/exec"
	"github.com/vitzeno/detent/internal/extract"
	"github.com/vitzeno/detent/internal/gate"
	"github.com/vitzeno/detent/internal/reduce"
)

// scriptedJudge returns one canned Answers per call, in call order —
// enough to script deterministic multi-step scenarios with no network
// call. Fails the test loudly if the loop asks for more calls than were
// scripted, rather than silently zero-valuing them.
type scriptedJudge struct {
	t         *testing.T
	responses []classify.Answers
	Calls     []classify.Questions // captures what was asked, for assertions
}

func (j *scriptedJudge) Ask(_ context.Context, _ classify.State, qs classify.Questions) (classify.Answers, classify.Usage, error) {
	j.Calls = append(j.Calls, qs)
	if len(j.Calls) > len(j.responses) {
		j.t.Fatalf("scriptedJudge: call %d has no scripted response (only %d scripted)", len(j.Calls), len(j.responses))
	}
	return j.responses[len(j.Calls)-1], classify.Usage{}, nil
}

// fakeConstructor is a func-based Constructor — flexible enough for every
// scenario below without a new named type per test.
type fakeConstructor func(argType capabilities.ArgType, goalText string, state extract.State) ([]extract.Candidate, error)

func (f fakeConstructor) Candidates(_ context.Context, argType capabilities.ArgType, goalText string, state extract.State) ([]extract.Candidate, error) {
	return f(argType, goalText, state)
}

func noCandidates(capabilities.ArgType, string, extract.State) ([]extract.Candidate, error) {
	return nil, nil
}

// testCounters records how many times each test handler actually ran —
// what proves a decline or a write-budget stop genuinely prevented
// dispatch, rather than merely returning the right Termination by luck.
type testCounters struct {
	MutateCalls        int
	MutateWithPIDCalls int
}

// newTestLoop wires a Loop against hermetic test capabilities — no_args,
// with_path (one required path arg), mutate (destructive, no args), and
// mutate_with_pid (destructive, one required pid arg) — so oscillation,
// reachability, and mutation-gating logic all get exercised without ever
// touching capabilities/unix.yaml or the real filesystem beyond a temp
// dir. Reuses reduce's real text_summary/process_lines reducers (already
// generic).
func newTestLoop(t *testing.T, judge classify.Judge, constructor extract.Constructor, budgets Budgets) (*Loop, string, *testCounters) {
	t.Helper()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("hello"), 0o644))

	capReg := capabilities.NewRegistry()
	require.NoError(t, capReg.Load(capabilities.Domain{
		Domain: "test", AvailableWhen: "always",
		Actions: []capabilities.Action{
			{Name: "no_args", Danger: capabilities.DangerSafe, Produces: "text", Reducer: "text_summary"},
			{
				Name: "with_path", Danger: capabilities.DangerSafe, Produces: "text", Reducer: "text_summary",
				Args: []capabilities.Arg{{Name: "path", Type: capabilities.ArgPath, Required: true}},
			},
			{Name: "mutate", Danger: capabilities.DangerDestructive, Produces: "none", Reducer: "none"},
			{
				Name: "mutate_with_pid", Danger: capabilities.DangerDestructive, Produces: "none", Reducer: "none",
				Args: []capabilities.Arg{{Name: "pid", Type: capabilities.ArgPID, Required: true}},
			},
			// Real process_lines reducer, real "ps"-shaped output — this is
			// what makes a genuine end-to-end kill-like test possible: a
			// pid discovered here, resolved and gated in a later step,
			// with real owner data instead of a goal-text guess.
			{Name: "list_procs", Danger: capabilities.DangerSafe, Produces: "pid[]", Reducer: "process_lines"},
		},
	}))

	counters := &testCounters{}
	execReg := exec.NewRegistry()
	execReg.Register("test__no_args", func(context.Context, exec.Args) (exec.Result, error) {
		return exec.Result{Output: "ran no_args"}, nil
	})
	execReg.Register("test__with_path", func(_ context.Context, args exec.Args) (exec.Result, error) {
		return exec.Result{Output: "content of " + args["path"].(string)}, nil
	})
	execReg.Register("test__mutate", func(context.Context, exec.Args) (exec.Result, error) {
		counters.MutateCalls++
		return exec.Result{Output: "mutated"}, nil
	})
	execReg.Register("test__mutate_with_pid", func(_ context.Context, args exec.Args) (exec.Result, error) {
		counters.MutateWithPIDCalls++
		return exec.Result{Output: fmt.Sprintf("mutated pid %v", args["pid"])}, nil
	})
	execReg.Register("test__list_procs", func(context.Context, exec.Args) (exec.Result, error) {
		return exec.Result{Output: " 4821 alice   node\n 5140 root    launchd\n"}, nil
	})

	reduceReg := reduce.NewRegistry()
	reduce.RegisterUnix(reduceReg)

	l := &Loop{
		Capabilities: capReg,
		Exec:         execReg,
		Reduce:       reduceReg,
		Judge:        judge,
		Constructor:  constructor,
		Budgets:      budgets,
		PathRules:    gate.PathRules{AllowedRoots: []string{dir}},
		CurrentUser:  "alice",
	}
	return l, filePath, counters
}

func alwaysApprove(ConfirmRequest) bool { return true }
func alwaysDecline(ConfirmRequest) bool { return false }

func choiceAnswer(choice string, confidence float64) classify.Answer {
	return classify.Answer{Choice: choice, Confidence: confidence}
}

func noulAnswer(v float64) classify.Answer {
	return classify.Answer{Noul: v}
}

func TestLoop_Run_Termination(t *testing.T) {
	highConfidence := 0.95

	tests := []struct {
		name       string
		responses  []classify.Answers
		budgets    Budgets
		wantReason Reason
		wantSteps  int // len(state.Findings) expected
	}{
		{
			name: "goal already achieved on iteration one, nothing executes",
			responses: []classify.Answers{{
				"next_action":      choiceAnswer("done", highConfidence),
				"goal_achieved":    noulAnswer(0.99),
				"goal_satisfiable": noulAnswer(0.9),
			}},
			budgets:    DefaultBudgets,
			wantReason: ReasonGoalAchieved,
			wantSteps:  0,
		},
		{
			name: "done selected explicitly",
			responses: []classify.Answers{{
				"next_action":      choiceAnswer("done", highConfidence),
				"goal_achieved":    noulAnswer(0.3),
				"goal_satisfiable": noulAnswer(0.9),
			}},
			budgets:    DefaultBudgets,
			wantReason: ReasonDone,
			wantSteps:  0,
		},
		{
			name: "cannot_proceed selected explicitly",
			responses: []classify.Answers{{
				"next_action":      choiceAnswer("cannot_proceed", highConfidence),
				"goal_achieved":    noulAnswer(0.1),
				"goal_satisfiable": noulAnswer(0.9),
			}},
			budgets:    DefaultBudgets,
			wantReason: ReasonCannotProceed,
			wantSteps:  0,
		},
		{
			name: "goal_satisfiable overrides next_action's own pick",
			responses: []classify.Answers{{
				"next_action":      choiceAnswer("test__no_args", highConfidence),
				"goal_achieved":    noulAnswer(0.1),
				"goal_satisfiable": noulAnswer(0.2), // below GoalSatisfiableFloor
			}},
			budgets:    DefaultBudgets,
			wantReason: ReasonGoalUnsatisfiable,
			wantSteps:  0,
		},
		{
			name: "low next_action confidence stops rather than guessing",
			responses: []classify.Answers{{
				"next_action":      choiceAnswer("test__no_args", 0.5), // below NextActionConfidenceFloor
				"goal_achieved":    noulAnswer(0.1),
				"goal_satisfiable": noulAnswer(0.9),
			}},
			budgets:    DefaultBudgets,
			wantReason: ReasonLowConfidence,
			wantSteps:  0,
		},
		{
			name: "step budget exhausted after repeated real work",
			responses: []classify.Answers{
				{ // step 1: no_args
					"next_action":   choiceAnswer("test__no_args", highConfidence),
					"goal_achieved": noulAnswer(0.1), "goal_satisfiable": noulAnswer(0.9),
				},
			},
			budgets:    Budgets{Steps: 1, StateTokens: 8000},
			wantReason: ReasonStepBudget,
			wantSteps:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			judge := &scriptedJudge{t: t, responses: tt.responses}
			l, _, _ := newTestLoop(t, judge, fakeConstructor(noCandidates), tt.budgets)

			state, term, err := l.Run(context.Background(), "some goal")
			require.NoError(t, err)
			assert.Equal(t, tt.wantReason, term.Reason)
			assert.Len(t, state.Findings, tt.wantSteps)
		})
	}
}

func TestLoop_Run_MultiStep_AccumulatesStateAndResolvesPath(t *testing.T) {
	responses := []classify.Answers{
		{ // step 1: run the no-arg capability
			"next_action":      choiceAnswer("test__no_args", 0.95),
			"goal_achieved":    noulAnswer(0.1),
			"goal_satisfiable": noulAnswer(0.9),
		},
		{ // step 2: goal now achieved, don't run with_path even though offered
			"next_action":      choiceAnswer("done", 0.95),
			"goal_achieved":    noulAnswer(0.99),
			"goal_satisfiable": noulAnswer(0.9),
		},
	}
	judge := &scriptedJudge{t: t, responses: responses}
	l, _, _ := newTestLoop(t, judge, fakeConstructor(noCandidates), DefaultBudgets)

	state, term, err := l.Run(context.Background(), "do the no-arg thing")
	require.NoError(t, err)
	assert.Equal(t, ReasonGoalAchieved, term.Reason)
	require.Len(t, state.Findings, 1)
	assert.Equal(t, "test__no_args", state.Findings[0].Action)
	assert.Equal(t, 1, state.Findings[0].Step)

	// oscillation: test__no_args must not be offered again on step 2
	require.Len(t, judge.Calls, 2)
	secondCallCriteria := judge.Calls[1]["next_action"].Choice.Criteria
	assert.NotContains(t, secondCallCriteria, "test__no_args")
}

func TestLoop_Run_ResolvesPathArgAndGates(t *testing.T) {
	l, filePath, _ := newTestLoop(t, nil, nil, DefaultBudgets)

	candidate := extract.Candidate{ID: "c1", Desc: filePath, Fields: map[string]any{"path": filePath}}
	l.Constructor = fakeConstructor(func(argType capabilities.ArgType, _ string, _ extract.State) ([]extract.Candidate, error) {
		if argType != capabilities.ArgPath {
			return nil, nil
		}
		return []extract.Candidate{candidate}, nil
	})

	judge := &scriptedJudge{t: t, responses: []classify.Answers{
		{ // step 1: pick with_path, resolve to the only candidate
			"next_action":            choiceAnswer("test__with_path", 0.95),
			"goal_achieved":          noulAnswer(0.1),
			"goal_satisfiable":       noulAnswer(0.9),
			"path_target":            choiceAnswer("c1", 0.9),
			"path_target_resolvable": noulAnswer(0.95),
		},
		{ // step 2: done
			"next_action":      choiceAnswer("done", 0.95),
			"goal_achieved":    noulAnswer(0.99),
			"goal_satisfiable": noulAnswer(0.9),
		},
	}}
	l.Judge = judge

	state, term, err := l.Run(context.Background(), "read notes.txt")
	require.NoError(t, err)
	assert.Equal(t, ReasonGoalAchieved, term.Reason)
	require.Len(t, state.Findings, 1)
	assert.Equal(t, "test__with_path", state.Findings[0].Action)

	require.Len(t, state.Resolved, 1)
	// ValidatePath resolves symlinks (gate.go) — on macOS /var is itself a
	// symlink to /private/var, so the resolved value legitimately differs
	// from the raw t.TempDir() path by that prefix. Compare against the
	// same resolution rather than the raw string.
	wantPath, err := filepath.EvalSymlinks(filePath)
	require.NoError(t, err)
	assert.Equal(t, wantPath, state.Resolved[0].Value)
	assert.Equal(t, "path", state.Resolved[0].Arg)
	assert.Equal(t, 0.95, state.Resolved[0].TargetResolvableNoul)
}

func TestLoop_Run_AmbiguousTargetStopsWithoutExecuting(t *testing.T) {
	l, filePath, _ := newTestLoop(t, nil, nil, DefaultBudgets)
	candidate := extract.Candidate{ID: "c1", Desc: filePath, Fields: map[string]any{"path": filePath}}
	l.Constructor = fakeConstructor(func(argType capabilities.ArgType, _ string, _ extract.State) ([]extract.Candidate, error) {
		if argType != capabilities.ArgPath {
			return nil, nil
		}
		return []extract.Candidate{candidate}, nil
	})

	judge := &scriptedJudge{t: t, responses: []classify.Answers{
		{
			"next_action":            choiceAnswer("test__with_path", 0.95),
			"goal_achieved":          noulAnswer(0.1),
			"goal_satisfiable":       noulAnswer(0.9),
			"path_target":            choiceAnswer("c1", 0.6),
			"path_target_resolvable": noulAnswer(0.2), // below TargetResolvableFloor
		},
	}}
	l.Judge = judge

	state, term, err := l.Run(context.Background(), "read the file")
	require.NoError(t, err)
	assert.Equal(t, ReasonAmbiguousTarget, term.Reason)
	assert.Empty(t, state.Findings)
	assert.Empty(t, state.Resolved)
}

func TestLoop_Run_StateBudgetExceeded(t *testing.T) {
	judge := &scriptedJudge{t: t, responses: []classify.Answers{{
		"next_action":      choiceAnswer("test__no_args", 0.95),
		"goal_achieved":    noulAnswer(0.1),
		"goal_satisfiable": noulAnswer(0.9),
	}}}
	// A state-token budget of 0 means even the empty starting state is
	// already "over budget" — proves the check runs before any call.
	l, _, _ := newTestLoop(t, judge, fakeConstructor(noCandidates), Budgets{Steps: 8, StateTokens: 0})

	state, term, err := l.Run(context.Background(), "goal")
	require.NoError(t, err)
	assert.Equal(t, ReasonStateBudget, term.Reason)
	assert.Empty(t, state.Findings)
	assert.Empty(t, judge.Calls) // never even asked
}

func TestLoop_Run_Mutation_ApprovedRunsAndCountsAgainstWriteBudget(t *testing.T) {
	judge := &scriptedJudge{t: t, responses: []classify.Answers{
		{ // step 1: mutate, approved
			"next_action":      choiceAnswer("test__mutate", 0.95),
			"goal_achieved":    noulAnswer(0.1),
			"goal_satisfiable": noulAnswer(0.9),
		},
		{ // step 2: done
			"next_action":      choiceAnswer("done", 0.95),
			"goal_achieved":    noulAnswer(0.99),
			"goal_satisfiable": noulAnswer(0.9),
		},
	}}
	l, _, counters := newTestLoop(t, judge, fakeConstructor(noCandidates), DefaultBudgets)

	var gotReq ConfirmRequest
	l.Confirm = func(req ConfirmRequest) bool { gotReq = req; return true }

	state, term, err := l.Run(context.Background(), "mutate something")
	require.NoError(t, err)
	assert.Equal(t, ReasonGoalAchieved, term.Reason)
	assert.Equal(t, 1, counters.MutateCalls)
	require.Len(t, state.Findings, 1)

	assert.Equal(t, "mutate something", gotReq.Goal)
	assert.Equal(t, "test__mutate", gotReq.Capability)
	assert.Equal(t, capabilities.DangerDestructive, gotReq.Danger)
	assert.Equal(t, 0, gotReq.WritesUsed) // shown *before* this write is counted
	assert.Equal(t, DefaultBudgets.Writes, gotReq.WriteBudget)
	assert.Empty(t, gotReq.Done) // nothing had run yet when this was asked
}

func TestLoop_Run_Mutation_DeclinedStopsEntireRun(t *testing.T) {
	judge := &scriptedJudge{t: t, responses: []classify.Answers{{
		"next_action":      choiceAnswer("test__mutate", 0.95),
		"goal_achieved":    noulAnswer(0.1),
		"goal_satisfiable": noulAnswer(0.9),
	}}}
	l, _, counters := newTestLoop(t, judge, fakeConstructor(noCandidates), DefaultBudgets)
	l.Confirm = alwaysDecline

	state, term, err := l.Run(context.Background(), "mutate something")
	require.NoError(t, err)
	assert.Equal(t, ReasonDeclined, term.Reason)
	assert.Equal(t, 0, counters.MutateCalls, "declined mutation must never dispatch")
	assert.Empty(t, state.Findings)
}

func TestLoop_Run_Mutation_NoConfirmConfigured_Errors(t *testing.T) {
	judge := &scriptedJudge{t: t, responses: []classify.Answers{{
		"next_action":      choiceAnswer("test__mutate", 0.95),
		"goal_achieved":    noulAnswer(0.1),
		"goal_satisfiable": noulAnswer(0.9),
	}}}
	l, _, counters := newTestLoop(t, judge, fakeConstructor(noCandidates), DefaultBudgets)
	l.Confirm = nil // deliberately unconfigured

	_, _, err := l.Run(context.Background(), "mutate something")
	assert.Error(t, err)
	assert.Equal(t, 0, counters.MutateCalls)
}

func TestLoop_Run_Mutation_WriteBudgetStopsBeforeResolvingArgsOrAskingConfirm(t *testing.T) {
	pidCandidate := extract.Candidate{
		ID: "pid_4821", Desc: "pid 4821, node, owned by alice",
		Fields: map[string]any{"pid": 4821, "owner": "alice"},
	}
	judge := &scriptedJudge{t: t, responses: []classify.Answers{
		{ // step 1: mutate, approved — spends the one write allowed
			"next_action":      choiceAnswer("test__mutate", 0.95),
			"goal_achieved":    noulAnswer(0.1),
			"goal_satisfiable": noulAnswer(0.9),
		},
		{ // step 2: a second, different mutation is picked, but the write
			// budget should stop it before pid_target is even resolved
			"next_action":           choiceAnswer("test__mutate_with_pid", 0.95),
			"goal_achieved":         noulAnswer(0.1),
			"goal_satisfiable":      noulAnswer(0.9),
			"pid_target":            choiceAnswer("pid_4821", 0.9),
			"pid_target_resolvable": noulAnswer(0.95),
		},
	}}
	constructor := fakeConstructor(func(argType capabilities.ArgType, _ string, _ extract.State) ([]extract.Candidate, error) {
		if argType != capabilities.ArgPID {
			return nil, nil
		}
		return []extract.Candidate{pidCandidate}, nil
	})
	l, _, counters := newTestLoop(t, judge, constructor, Budgets{Steps: 8, StateTokens: 8000, Writes: 1})

	confirmCalls := 0
	l.Confirm = func(ConfirmRequest) bool { confirmCalls++; return true }

	state, term, err := l.Run(context.Background(), "mutate twice")
	require.NoError(t, err)
	assert.Equal(t, ReasonWriteBudget, term.Reason)
	assert.Equal(t, 1, confirmCalls, "confirm should only be asked once, for the approved write")
	assert.Equal(t, 1, counters.MutateCalls)
	assert.Equal(t, 0, counters.MutateWithPIDCalls, "the write-budget-blocked mutation must never dispatch")
	require.Len(t, state.Findings, 1)
}

func TestLoop_Run_KillLikeFlow_ResolvesPIDViaRealProcessListStep(t *testing.T) {
	// Mirrors the real unix__process_list -> unix__kill_process shape:
	// step 1 discovers real processes (with owners, via the real
	// process_lines reducer), step 2 resolves pid_target against that
	// discovered, owned data — never a goal-text literal — and
	// gate.ValidatePID succeeds because pid 4821 is both present and
	// owned by CurrentUser ("alice", set in newTestLoop). pid 5140
	// (owned by "root") is deliberately also in the process list, to
	// prove the right one gets picked, not just any known pid.
	judge := &scriptedJudge{t: t, responses: []classify.Answers{
		{ // step 1: list_procs
			"next_action":      choiceAnswer("test__list_procs", 0.95),
			"goal_achieved":    noulAnswer(0.1),
			"goal_satisfiable": noulAnswer(0.9),
		},
		{ // step 2: mutate_with_pid, resolved against the discovered pid
			"next_action":           choiceAnswer("test__mutate_with_pid", 0.95),
			"goal_achieved":         noulAnswer(0.1),
			"goal_satisfiable":      noulAnswer(0.9),
			"pid_target":            choiceAnswer("pid_4821", 0.9),
			"pid_target_resolvable": noulAnswer(0.95),
		},
		{ // step 3: done
			"next_action":      choiceAnswer("done", 0.95),
			"goal_achieved":    noulAnswer(0.99),
			"goal_satisfiable": noulAnswer(0.9),
		},
	}}
	l, _, counters := newTestLoop(t, judge, nil, DefaultBudgets)
	// A real Constructor here (not a fake): candidatesFromState is what
	// actually turns step 1's discovered process_lines Values into
	// pid_target's candidates — the same code path unix's real handlers
	// go through.
	l.Constructor = &extract.DeterministicConstructor{}
	l.Confirm = alwaysApprove

	state, term, err := l.Run(context.Background(), "kill node")
	require.NoError(t, err)
	assert.Equal(t, ReasonGoalAchieved, term.Reason)
	require.Equal(t, 1, counters.MutateWithPIDCalls)
	require.Len(t, state.Findings, 2)

	require.Len(t, state.Resolved, 1)
	assert.Equal(t, 4821, state.Resolved[0].Value)
	assert.Equal(t, "test__mutate_with_pid", state.Resolved[0].Capability)
}

func TestLoop_Run_PIDFromLiteralWithoutOwner_IsGateRejected(t *testing.T) {
	// The dangerous case §7 exists to prevent: a pid named only in the
	// goal's own wording, never verified against a real process list,
	// must never reach the handler — even if next_action, pid_target, and
	// pid_target_resolvable are all maximally confident.
	pidCandidate := extract.Candidate{
		ID: "pid_4821", Desc: "pid 4821 (from the goal text — not yet verified against a real process)",
		Fields: map[string]any{"pid": 4821}, // no "owner" — never checked against reality
	}
	judge := &scriptedJudge{t: t, responses: []classify.Answers{{
		"next_action":           choiceAnswer("test__mutate_with_pid", 0.95),
		"goal_achieved":         noulAnswer(0.1),
		"goal_satisfiable":      noulAnswer(0.9),
		"pid_target":            choiceAnswer("pid_4821", 0.99),
		"pid_target_resolvable": noulAnswer(0.99),
	}}}
	constructor := fakeConstructor(func(argType capabilities.ArgType, _ string, _ extract.State) ([]extract.Candidate, error) {
		if argType != capabilities.ArgPID {
			return nil, nil
		}
		return []extract.Candidate{pidCandidate}, nil
	})
	l, _, counters := newTestLoop(t, judge, constructor, DefaultBudgets)
	l.Confirm = alwaysApprove

	_, _, err := l.Run(context.Background(), "kill pid 4821")
	assert.Error(t, err)
	assert.Equal(t, 0, counters.MutateWithPIDCalls, "an unverified pid must never reach the handler")
}
