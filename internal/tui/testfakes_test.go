package tui

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capabilities"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/exec"
	"github.com/vitzeno/detent/internal/extract"
	"github.com/vitzeno/detent/internal/gate"
	"github.com/vitzeno/detent/internal/loop"
	"github.com/vitzeno/detent/internal/reduce"
)

// scriptedJudge returns one canned Answers per call, in order — mirrors
// internal/loop's own test double (unexported there, so duplicated here;
// this package tests Model/Update, not loop's internals, so it only
// needs the Judge interface, not loop's other test machinery).
type scriptedJudge struct {
	t         *testing.T
	responses []classify.Answers
}

func (j *scriptedJudge) Ask(_ context.Context, _ classify.State, _ classify.Questions) (classify.Answers, classify.Usage, error) {
	if len(j.responses) == 0 {
		j.t.Fatalf("scriptedJudge: no more scripted responses")
	}
	r := j.responses[0]
	j.responses = j.responses[1:]
	return r, classify.Usage{}, nil
}

type fakeConstructor func(argType capabilities.ArgType) ([]extract.Candidate, error)

func (f fakeConstructor) Candidates(_ context.Context, argType capabilities.ArgType, _ string, _ extract.State) ([]extract.Candidate, error) {
	return f(argType)
}

func noCandidates(capabilities.ArgType) ([]extract.Candidate, error) { return nil, nil }

func choiceAnswer(choice string, confidence float64) classify.Answer {
	return classify.Answer{Choice: choice, Confidence: confidence}
}

func noulAnswer(v float64) classify.Answer {
	return classify.Answer{Noul: v}
}

// newTestLoop wires a *loop.Loop against two hermetic test capabilities —
// no_args (safe) and mutate (destructive, no args) — enough to exercise
// every screen transition without touching real capabilities/unix.yaml.
func newTestLoop(t *testing.T, judge classify.Judge, constructor extract.Constructor) *loop.Loop {
	t.Helper()

	capReg := capabilities.NewRegistry()
	require.NoError(t, capReg.Load(capabilities.Domain{
		Domain: "test", AvailableWhen: "always",
		Actions: []capabilities.Action{
			{Name: "no_args", Danger: capabilities.DangerSafe, Produces: "text", Reducer: "text_summary"},
			{Name: "mutate", Danger: capabilities.DangerDestructive, Produces: "none", Reducer: "none"},
			{
				Name: "with_path", Danger: capabilities.DangerSafe, Produces: "text", Reducer: "text_summary",
				Args: []capabilities.Arg{{Name: "path", Type: capabilities.ArgPath, Required: true}},
			},
		},
	}))

	execReg := exec.NewRegistry()
	execReg.Register("test__no_args", func(context.Context, exec.Args) (exec.Result, error) {
		return exec.Result{Output: "ran no_args"}, nil
	})
	execReg.Register("test__mutate", func(context.Context, exec.Args) (exec.Result, error) {
		return exec.Result{Output: "mutated"}, nil
	})
	execReg.Register("test__with_path", func(_ context.Context, args exec.Args) (exec.Result, error) {
		return exec.Result{Output: "content of " + args["path"].(string)}, nil
	})

	reduceReg := reduce.NewRegistry()
	reduce.RegisterUnix(reduceReg)

	return &loop.Loop{
		Capabilities: capReg,
		Exec:         execReg,
		Reduce:       reduceReg,
		Judge:        judge,
		Constructor:  constructor,
		Budgets:      loop.DefaultBudgets,
		PathRules:    gate.PathRules{AllowedRoots: []string{"/"}},
		CurrentUser:  "alice",
	}
}
