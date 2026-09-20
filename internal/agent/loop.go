package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/probe"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/usage"
)

// probeRunner adapts a Runner to probe.Runner; probes never stream.
type probeRunner struct {
	runner Runner
}

func (r probeRunner) Run(ctx context.Context, command string) (host.Result, error) {
	return r.runner.Run(ctx, command, nil)
}

// BeginGoal opens a goal and primes the transcript with fixed, unconfirmed
// probes; only RunGoal fails closed without Confirm.
func (s *Session) BeginGoal(ctx context.Context, goal string) (*GoalResult, error) {
	if strings.TrimSpace(goal) == "" {
		return nil, fmt.Errorf("agent: empty goal")
	}
	if s.Proposer == nil {
		return nil, fmt.Errorf("agent: no Proposer wired")
	}
	if s.Run == nil {
		return nil, fmt.Errorf("agent: no Runner wired")
	}
	s.append(propose.Message{Role: propose.RoleUser, Content: goal})
	prober := probe.New(probeRunner{runner: s.Run})
	if out := prober.Run(ctx, probe.Select(ctx, s.Judge, goal)); out != "" {
		s.append(propose.Message{Role: propose.RoleTool, Content: out})
	}
	// Abort during probe collection must close the goal, not fall
	// through into the first Propose.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	res := &GoalResult{Goal: goal}
	res.Stats = s.Stats.StartGoal(goal)
	return res, nil
}

// ProposeNext returns one proposal, its pre-confirm judgment, and what
// the propose call consumed.
func (s *Session) ProposeNext(ctx context.Context, goal string) (propose.Proposal, PreJudgment, usage.Usage, error) {
	if err := ctx.Err(); err != nil {
		return propose.Proposal{}, PreJudgment{}, usage.Usage{}, err
	}
	p, used, err := s.Proposer.Propose(ctx, s.Transcript)
	if err != nil {
		return propose.Proposal{}, PreJudgment{}, used, fmt.Errorf("agent: %w", err)
	}
	if p.Done {
		return p, PreJudgment{}, used, nil
	}
	return p, s.judgePre(ctx, goal, p.Command), used, nil
}

func (s *Session) RecordDone(res *GoalResult, p propose.Proposal) {
	res.Summary = p.Summary
	s.append(propose.Message{Role: propose.RoleAssistant,
		Content: propose.EncodeAssistantTurn(p)})
	s.finish(res, EndDone, p.Summary)
}

// RecordFileSave notes a direct editor write (not a proposed command).
// Not goal-scoped — this happens outside RunGoal/BeginGoal entirely.
func (s *Session) RecordFileSave(path, diff string) {
	s.append(propose.Message{Role: propose.RoleTool, Content: fmt.Sprintf(
		"The human edited `%s` directly in the editor and saved this change:\n%s", path, boundStr(diff))})
}

func (s *Session) RecordDecline(res *GoalResult, command string) {
	s.append(propose.Message{Role: propose.RoleUser, Content: fmt.Sprintf(
		"[goal ended by human: declined command `%s` after %d command(s)]",
		command, len(res.Commands))})
	s.finish(res, EndDeclined, "")
}

func (s *Session) RecordBudget(res *GoalResult, budget int) {
	s.append(propose.Message{Role: propose.RoleUser, Content: fmt.Sprintf(
		"[goal ended: step budget exhausted (%d/%d), goal not confirmed done]", budget, budget)})
	s.finish(res, EndBudget, "")
}

func (s *Session) RecordProposerError(res *GoalResult, err error) {
	s.append(propose.Message{Role: propose.RoleUser, Content: fmt.Sprintf(
		"[goal ended: proposer error: %v]", err)})
	s.finish(res, EndProposerError, "")
}

// RecordAbort notes the human aborted. res is nil if BeginGoal hadn't
// produced one yet.
func (s *Session) RecordAbort(res *GoalResult) {
	s.append(propose.Message{Role: propose.RoleUser, Content: "[goal ended by human: aborted]"})
	if res == nil {
		return
	}
	s.finish(res, EndAborted, "")
}

// Execute runs an approved proposal, times it, and records the outcome.
// Post stays nil until the caller attaches JudgeResult. events may be nil.
func (s *Session) Execute(ctx context.Context, res *GoalResult, ustep *usage.Step, p propose.Proposal, pre PreJudgment, events chan<- host.StreamEvent) (*ExecutedCommand, error) {
	t0 := time.Now()
	outcome, err := s.Run.Run(ctx, p.Command, events)
	elapsed := time.Since(t0)
	if err != nil {
		s.append(propose.Message{Role: propose.RoleAssistant,
			Content: propose.EncodeAssistantTurn(p)})
		s.append(propose.Message{Role: propose.RoleTool,
			Content: fmt.Sprintf("Command `%s` failed to execute: %v", p.Command, err)})
		ec := &ExecutedCommand{Command: p.Command, Pre: pre, Usage: ustep}
		res.Commands = append(res.Commands, ec)
		reason := EndProposerError
		if ctx.Err() == context.Canceled {
			reason = EndAborted
		}
		s.finish(res, reason, "")
		return nil, fmt.Errorf("agent: run %q: %w", p.Command, err)
	}
	ec := &ExecutedCommand{Command: p.Command, Result: outcome, Pre: pre, Usage: ustep}
	ustep.SetExec(elapsed, outcome.ExitCode, len(outcome.Stdout)+len(outcome.Stderr))
	res.Commands = append(res.Commands, ec)
	s.append(propose.Message{Role: propose.RoleAssistant,
		Content: propose.EncodeAssistantTurn(p)})
	s.append(propose.Message{Role: propose.RoleTool, Content: formatToolResult(p.Command, outcome)})
	return ec, nil
}

// JudgeResult judges; judgments never enter the transcript. Falls back
// to a heuristic without a Judge. Updates step directly.
func (s *Session) JudgeResult(ctx context.Context, goal, command string, result host.Result, step *usage.Step) PostJudgment {
	var out string
	if result.Stdout != "" {
		out = result.Stdout
	}
	if result.Stderr != "" {
		if out != "" {
			out += "\n"
		}
		out += result.Stderr
	}
	if len(out) > MaxTranscriptOutputBytes {
		out = out[:MaxTranscriptOutputBytes] + "\n…[truncated]"
	}
	post := s.judgePost(ctx, goal, command, resultView{
		ExitCode: result.ExitCode,
		Output:   out,
		Lines:    countLines(result.Stdout) + countLines(result.Stderr),
	})
	step.SetJudgePost(post.JudgeUsage, post.Attention, post.GoalAchieved)
	return post
}

// RecordStep opens usage bookkeeping, shared by RunGoal and resolver.
// dwell > 0 only when a confirm was shown.
func (s *Session) RecordStep(res *GoalResult, p propose.Proposal, pre PreJudgment, proposeUsage usage.Usage, dwell time.Duration) *usage.Step {
	ustep := res.Stats.AddStep(p.Command)
	ustep.SetPropose(proposeUsage)
	ustep.SetJudgePre(pre.JudgeUsage)
	if dwell > 0 {
		ustep.SetDwell(dwell)
	}
	return ustep
}

// RunGoal works one goal to completion; unbounded unless StepBudget is set.
func (s *Session) RunGoal(ctx context.Context, goal string) (GoalResult, error) {
	if s.Confirm == nil {
		// Fail closed without Confirm.
		s.append(propose.Message{Role: propose.RoleUser, Content: goal})
		s.append(propose.Message{Role: propose.RoleUser,
			Content: "[goal ended without running: no confirm function wired]"})
		res := GoalResult{Goal: goal, End: EndConfirmMissing}
		res.Stats = s.Stats.StartGoal(goal)
		res.Stats.Finish(string(res.End), "", false)
		return res, fmt.Errorf("agent: no Confirm function wired, refusing to run")
	}
	res, err := s.BeginGoal(ctx, goal)
	if err != nil {
		return GoalResult{}, err
	}

	budget, hasBudget := s.bounded()
	confirmBudget := 0
	if hasBudget {
		confirmBudget = budget
	}

	for step := 1; ; step++ {
		if hasBudget && step > budget {
			s.RecordBudget(res, budget)
			return *res, nil
		}

		proposal, pre, used, err := s.ProposeNext(ctx, goal)
		if err != nil {
			if ctx.Err() != nil {
				return *res, fmt.Errorf("agent: %w", ctx.Err())
			}
			s.RecordProposerError(res, err)
			return *res, err
		}

		if proposal.Done {
			s.RecordDone(res, proposal)
			return *res, nil
		}

		// Confirm is only shown for a command Dangerous flags.
		approved := true
		var dwell time.Duration
		if pre.Dangerous {
			t0 := time.Now()
			approved = s.Confirm.Confirm(ConfirmRequest{
				Goal:       goal,
				Command:    proposal.Command,
				Rationale:  proposal.Rationale,
				Dangerous:  pre.Dangerous,
				RiskNote:   pre.RiskNote,
				Mutability: pre.Mutability,
				Step:       step,
				StepBudget: confirmBudget,
				History:    append([]*ExecutedCommand(nil), res.Commands...),
				GoalsDone:  s.GoalsDone,
			})
			dwell = time.Since(t0)
		}
		ustep := s.RecordStep(res, proposal, pre, used, dwell)
		if !approved {
			s.RecordDecline(res, proposal.Command)
			return *res, nil
		}

		ec, err := s.Execute(ctx, res, ustep, proposal, pre, nil)
		if err != nil {
			return *res, err
		}
		post := s.JudgeResult(ctx, goal, ec.Command, ec.Result, ustep)
		ec.Post = &post
	}
}
