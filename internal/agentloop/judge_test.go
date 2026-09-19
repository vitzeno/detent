package agentloop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/shell"
)

func TestRunGoal_UnboundedByDefault(t *testing.T) {
	script := make([]propose.Proposal, 0, 9)
	for i := 0; i < 8; i++ {
		script = append(script, propose.Proposal{Command: "echo hi", Rationale: "hi"})
	}
	script = append(script, propose.Proposal{Done: true, Summary: "done"})
	s := &Session{
		Proposer: &stubProposer{script: script},
		Confirm:  func(ConfirmRequest) bool { return true },
		Run:      okRun(shell.Result{Stdout: "hi\n"}),
	}

	res, err := s.RunGoal(context.Background(), "g")
	require.NoError(t, err)
	assert.Equal(t, EndDone, res.End)
	assert.Len(t, res.Commands, 8)
}

func TestRunGoal_ExplicitCapStillEnds(t *testing.T) {
	script := []propose.Proposal{
		{Command: "echo 1", Rationale: "one"},
		{Command: "echo 2", Rationale: "two"},
		{Command: "echo 3", Rationale: "three"},
	}
	s := &Session{
		Proposer:   &stubProposer{script: script},
		Confirm:    func(ConfirmRequest) bool { return true },
		Run:        okRun(shell.Result{}),
		StepBudget: 2,
	}
	res, err := s.RunGoal(context.Background(), "g")
	require.NoError(t, err)
	assert.Equal(t, EndBudget, res.End)
	assert.Len(t, res.Commands, 2)
}

func TestRunGoal_HeuristicPostAttached(t *testing.T) {
	stub := &stubProposer{script: []propose.Proposal{
		{Command: "ls", Rationale: "list"},
		{Done: true, Summary: "done"},
	}}
	s := &Session{
		Proposer: stub,
		Confirm:  func(ConfirmRequest) bool { return true },
		Run:      okRun(shell.Result{Stdout: "a\nb\n"}),
	}
	res, err := s.RunGoal(context.Background(), "g")
	require.NoError(t, err)
	require.Len(t, res.Commands, 1)
	require.NotNil(t, res.Commands[0].Post)
	assert.False(t, res.Commands[0].Post.FromJudge)
	assert.Equal(t, StatusClean, res.Commands[0].Post.Status)
	assert.Equal(t, KindInline, res.Commands[0].Post.RenderKind)
}

type batchJudge struct {
	preMutability string
	preRisk       float64
	status        string
	kind          string
	attention     float64
	achieved      float64
}

func (b *batchJudge) Ask(_ context.Context, _ classify.State, qs classify.Questions) (classify.Answers, classify.Usage, error) {
	out := classify.Answers{}
	if _, ok := qs["mutability"]; ok {
		out["mutability"] = classify.Answer{Choice: b.preMutability, Confidence: 0.8,
			Probabilities: map[string]float64{b.preMutability: 0.8}}
		out["scope_risk"] = classify.Answer{Noul: b.preRisk}
	}
	if _, ok := qs["result_status"]; ok {
		out["result_status"] = classify.Answer{Choice: b.status, Confidence: 0.9}
		out["render_kind"] = classify.Answer{Choice: b.kind}
		out["attention"] = classify.Answer{Noul: b.attention}
		out["goal_achieved"] = classify.Answer{Noul: b.achieved}
	}
	return out, classify.Usage{}, nil
}

func TestDriver_FullBatchFlowsThrough(t *testing.T) {
	stub := &stubProposer{script: []propose.Proposal{
		{Command: "ls", Rationale: "list"},
		{Done: true, Summary: "done"},
	}}
	var confirmedReq *ConfirmRequest
	s := &Session{
		Proposer: stub,
		Confirm: func(req ConfirmRequest) bool {
			cp := req
			confirmedReq = &cp
			return true
		},
		Run: okRun(shell.Result{Stdout: "a\n"}),
		Judge: &batchJudge{
			preMutability: MutReadOnly, preRisk: 0.1,
			status: StatusClean, kind: KindInline, attention: 0.2, achieved: 0.9,
		},
	}

	res, err := s.BeginGoal("g")
	require.NoError(t, err)
	p, gotPre, _, err := s.ProposeNext(context.Background(), "g")
	require.NoError(t, err)
	assert.True(t, gotPre.FromJudge)
	assert.Equal(t, MutReadOnly, gotPre.Mutability)
	assert.False(t, gotPre.Dangerous)

	approved := s.Confirm(ConfirmRequest{
		Goal: "g", Command: p.Command, Rationale: p.Rationale,
		Dangerous: gotPre.Dangerous, RiskNote: gotPre.RiskNote,
		Mutability: gotPre.Mutability,
	})
	require.True(t, approved)
	require.NotNil(t, confirmedReq)
	assert.Equal(t, MutReadOnly, confirmedReq.Mutability)

	ec, err := s.Execute(context.Background(), res, nil, p, gotPre, nil)
	require.NoError(t, err)
	post := s.JudgeResult(context.Background(), "g", ec.Command, ec.Result)
	ec.Post = &post
	require.NotNil(t, ec.Post)
	assert.True(t, ec.Post.FromJudge)
	assert.Equal(t, StatusClean, ec.Post.Status)
	assert.Equal(t, KindInline, ec.Post.RenderKind)
	assert.Equal(t, 0.9, ec.Post.GoalAchieved)
	assert.Equal(t, MutReadOnly, confirmedReq.Mutability)
}

func TestDriver_MutabilityEscalates(t *testing.T) {
	s := &Session{
		Proposer: &stubProposer{},
		Confirm:  func(ConfirmRequest) bool { return true },
		Judge:    &batchJudge{preMutability: MutIrreversible, preRisk: 0.0},
	}
	pre := s.judgePre(context.Background(), "g", "echo hi")
	assert.True(t, pre.FromJudge)
	assert.True(t, pre.Dangerous, "irreversible tier escalates even at zero scope risk")
	assert.Contains(t, pre.RiskNote, "likely_irreversible")
}

func TestHeuristicPost_Table(t *testing.T) {
	assert.Equal(t, StatusFailed, heuristicPost(resultView{ExitCode: 1, Lines: 3}).Status)
	assert.Equal(t, KindError, heuristicPost(resultView{ExitCode: 1}).RenderKind)
	assert.Equal(t, StatusEmpty, heuristicPost(resultView{Lines: 0}).Status)
	assert.Equal(t, KindInline, heuristicPost(resultView{Lines: 5}).RenderKind)
	assert.Equal(t, KindLog, heuristicPost(resultView{Lines: 50}).RenderKind)
}
