package resolver

import (
	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/ui"
	"github.com/vitzeno/detent/internal/usage"
)

// Pure one-way mapping functions between agent's/propose's/usage's/
// host's domain types and ui's own DTOs. EndReason and RenderKind are
// identical string sets on both sides, so those are plain type
// conversions, not lookup tables.

func toProposal(p propose.Proposal) ui.Proposal {
	return ui.Proposal{Command: p.Command, Rationale: p.Rationale, Done: p.Done, Summary: p.Summary, File: p.File}
}

func fromProposal(p ui.Proposal) propose.Proposal {
	return propose.Proposal{Command: p.Command, Rationale: p.Rationale, Done: p.Done, Summary: p.Summary, File: p.File}
}

func toPreJudgment(p agent.PreJudgment) ui.PreJudgment {
	return ui.PreJudgment{
		FromJudge:  p.FromJudge,
		Mutability: p.Mutability,
		ScopeRisk:  p.ScopeRisk,
		Dangerous:  p.Dangerous,
		RiskNote:   p.RiskNote,
	}
}

func fromPreJudgment(p ui.PreJudgment) agent.PreJudgment {
	return agent.PreJudgment{
		FromJudge:  p.FromJudge,
		Mutability: p.Mutability,
		ScopeRisk:  p.ScopeRisk,
		Dangerous:  p.Dangerous,
		RiskNote:   p.RiskNote,
	}
}

func toPostJudgment(p agent.PostJudgment) ui.PostJudgment {
	return ui.PostJudgment{
		FromJudge:    p.FromJudge,
		Status:       p.Status,
		RenderKind:   ui.RenderKind(p.RenderKind),
		Attention:    p.Attention,
		GoalAchieved: p.GoalAchieved,
	}
}

func toUsage(u usage.Usage) ui.Usage {
	return ui.Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, Latency: u.Latency, Model: u.Model}
}

func fromUsage(u ui.Usage) usage.Usage {
	return usage.Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, Latency: u.Latency, Model: u.Model}
}

func toResult(r host.Result) ui.Result {
	return ui.Result{Stdout: r.Stdout, Stderr: r.Stderr, ExitCode: r.ExitCode, Truncated: r.Truncated}
}

func fromResult(r ui.Result) host.Result {
	return host.Result{Stdout: r.Stdout, Stderr: r.Stderr, ExitCode: r.ExitCode, Truncated: r.Truncated}
}

func toExecutedCommand(ec *agent.ExecutedCommand) *ui.ExecutedCommand {
	if ec == nil {
		return nil
	}
	out := &ui.ExecutedCommand{
		Command: ec.Command,
		Result:  toResult(ec.Result),
		Pre:     toPreJudgment(ec.Pre),
	}
	if ec.Post != nil {
		post := toPostJudgment(*ec.Post)
		out.Post = &post
	}
	return out
}

// toGoalResult wraps a freshly begun goal. Ref carries the live
// *agent.GoalResult so later calls (RecordStep, Execute, Record*) can
// find it again via goalRef, with no side-table needed.
func toGoalResult(res *agent.GoalResult) *ui.GoalResult {
	return &ui.GoalResult{Goal: res.Goal, End: ui.EndReason(res.End), Ref: res}
}

// goalRef recovers the live *agent.GoalResult a ui.GoalResult was built
// from. Panics on a foreign or zero-value GoalResult — that means a
// caller passed back something this Driver never handed it.
func goalRef(res *ui.GoalResult) *agent.GoalResult {
	return res.Ref.(*agent.GoalResult)
}

// syncGoalResult copies End/Summary back onto the ui-owned DTO after a
// Record* call changes them. Commands isn't touched here — it only
// grows via one append in Execute, which keeps a caller's own
// *ui.ExecutedCommand pointer valid instead of orphaning it.
func syncGoalResult(uiRes *ui.GoalResult, res *agent.GoalResult) {
	uiRes.End = ui.EndReason(res.End)
	uiRes.Summary = res.Summary
}

func toGoalStats(g *usage.Goal) ui.GoalStats {
	steps := make([]ui.StepStats, len(g.Steps))
	for i, s := range g.Steps {
		steps[i] = ui.StepStats{
			Command:          s.Command,
			Propose:          s.Propose,
			Dwell:            s.Dwell,
			Exec:             s.Exec,
			JudgePost:        s.JudgePost,
			ExitCode:         s.ExitCode,
			ProposerPrompt:   s.ProposerPrompt,
			ProposerComplete: s.ProposerComplete,
			JudgePrompt:      s.JudgePrompt,
			JudgeComplete:    s.JudgeComplete,
			Attention:        s.Attention,
			GoalAchieved:     s.GoalAchieved,
			HasPost:          s.HasPost,
		}
	}
	return ui.GoalStats{Text: g.Text, End: g.End, Steps: steps}
}

func toSnapshot(s usage.Snapshot) ui.Snapshot {
	return ui.Snapshot{
		Goals:          s.Goals,
		Commands:       s.Commands,
		Declined:       s.Declined,
		Propose:        s.Propose,
		Judge:          s.Judge,
		Dwell:          s.Dwell,
		Exec:           s.Exec,
		ProposerTokens: s.ProposerTokens,
		JudgeTokens:    s.JudgeTokens,
	}
}
