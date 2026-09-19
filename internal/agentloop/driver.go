package agentloop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vitzeno/detent/internal/probe"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/shell"
	"github.com/vitzeno/detent/internal/usage"
)

// BeginGoal opens a goal; only RunGoal (not this) fails closed without Confirm.
// It also primes the transcript with whatever fixed, read-only probes
// Jev thinks are relevant (internal/probe) — never model-proposed, so
// they run without confirm, and appear to the proposer as one RoleTool
// message right after the goal, the same as any other command's output.
func (s *Session) BeginGoal(ctx context.Context, goal string) (*GoalResult, error) {
	if strings.TrimSpace(goal) == "" {
		return nil, fmt.Errorf("agentloop: empty goal")
	}
	if s.Proposer == nil {
		return nil, fmt.Errorf("agentloop: no Proposer wired")
	}
	s.append(propose.Message{Role: propose.RoleUser, Content: goal})
	if out := probe.Run(ctx, s.runFunc(), probe.Select(ctx, s.Judge, goal)); out != "" {
		s.append(propose.Message{Role: propose.RoleTool, Content: out})
	}
	// Abort during probe collection must behave like abort during
	// propose or exec: the caller (ui.onBeginGoal) closes the goal as
	// aborted rather than silently continuing into the first Propose.
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
		return propose.Proposal{}, PreJudgment{}, used, fmt.Errorf("agentloop: %w", err)
	}
	if p.Done {
		return p, PreJudgment{}, used, nil
	}
	return p, s.judgePre(ctx, goal, p.Command), used, nil
}

func (s *Session) RecordDone(res *GoalResult, p propose.Proposal) {
	res.End = EndDone
	res.Summary = p.Summary
	s.append(propose.Message{Role: propose.RoleAssistant,
		Content: propose.EncodeAssistantTurn(p)})
	res.Stats.Finish(string(res.End), p.Summary)
	s.GoalsDone++
}

// RecordFileSave notes that the human — not a proposed command — wrote
// path directly through the editor, so a later goal in this session
// sees what changed without needing to re-read the file itself. diff
// is the unified diff of what changed (editfile.Diff), not goal-scoped:
// this happens outside RunGoal/BeginGoal entirely, so there's no
// GoalResult to attach it to the way command output attaches to one.
func (s *Session) RecordFileSave(path, diff string) {
	s.append(propose.Message{Role: propose.RoleTool, Content: fmt.Sprintf(
		"The human edited `%s` directly in the editor and saved this change:\n%s", path, boundStr(diff))})
}

func (s *Session) RecordDecline(res *GoalResult, command string) {
	res.End = EndDeclined
	s.append(propose.Message{Role: propose.RoleUser, Content: fmt.Sprintf(
		"[goal ended by human: declined command `%s` after %d command(s)]",
		command, len(res.Commands))})
	res.Stats.Finish(string(res.End), "")
	s.GoalsDone++
}

func (s *Session) RecordBudget(res *GoalResult, budget int) {
	res.End = EndBudget
	s.append(propose.Message{Role: propose.RoleUser, Content: fmt.Sprintf(
		"[goal ended: step budget exhausted (%d/%d), goal not confirmed done]", budget, budget)})
	res.Stats.Finish(string(res.End), "")
	s.GoalsDone++
}

func (s *Session) RecordProposerError(res *GoalResult, err error) {
	res.End = EndProposerError
	s.append(propose.Message{Role: propose.RoleUser, Content: fmt.Sprintf(
		"[goal ended: proposer error: %v]", err)})
	res.Stats.Finish(string(res.End), "")
}

// Execute runs an approved proposal against its usage step: links the
// step, times the run, and records the outcome. Post stays nil until
// the caller attaches JudgeResult.
func (s *Session) Execute(ctx context.Context, res *GoalResult, ustep *usage.Step, p propose.Proposal, pre PreJudgment, onEvent func(shell.StreamEvent)) (*ExecutedCommand, error) {
	t0 := time.Now()
	outcome, err := s.runFunc()(ctx, p.Command, onEvent)
	elapsed := time.Since(t0)
	if err != nil {
		s.append(propose.Message{Role: propose.RoleAssistant,
			Content: propose.EncodeAssistantTurn(p)})
		s.append(propose.Message{Role: propose.RoleTool,
			Content: fmt.Sprintf("Command `%s` failed to execute: %v", p.Command, err)})
		ec := &ExecutedCommand{Command: p.Command, Pre: pre, Usage: ustep}
		res.Commands = append(res.Commands, ec)
		if ctx.Err() == context.Canceled {
			res.End = EndAborted
		} else {
			res.End = EndProposerError
		}
		res.Stats.Finish(string(res.End), "")
		s.GoalsDone++
		return nil, fmt.Errorf("agentloop: run %q: %w", p.Command, err)
	}
	ec := &ExecutedCommand{Command: p.Command, Result: outcome, Pre: pre, Usage: ustep}
	ustep.SetExec(elapsed, outcome.ExitCode, len(outcome.Stdout)+len(outcome.Stderr))
	res.Commands = append(res.Commands, ec)
	s.append(propose.Message{Role: propose.RoleAssistant,
		Content: propose.EncodeAssistantTurn(p)})
	s.append(propose.Message{Role: propose.RoleTool, Content: formatToolResult(p.Command, outcome)})
	return ec, nil
}

// JudgeResult takes values not a slice pointer (appends may move it); judgments never enter the transcript.
// Without a Judge or on error it returns the heuristic fallback.
func (s *Session) JudgeResult(ctx context.Context, goal, command string, result shell.Result) PostJudgment {
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
	return s.judgePost(ctx, goal, command, resultView{
		ExitCode: result.ExitCode,
		Output:   out,
		Lines:    countLines(result.Stdout) + countLines(result.Stderr),
	})
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
		res.Stats.Finish(string(res.End), "")
		return res, fmt.Errorf("agentloop: no Confirm function wired, refusing to run")
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
				return *res, fmt.Errorf("agentloop: %w", ctx.Err())
			}
			s.RecordProposerError(res, err)
			return *res, err
		}

		if proposal.Done {
			s.RecordDone(res, proposal)
			return *res, nil
		}

		ustep := res.Stats.AddStep(proposal.Command)
		ustep.SetPropose(used)
		ustep.SetJudgePre(pre.JudgeUsage)

		// Confirm is only shown for a command Dangerous flags — Jev's
		// mutability/scope_risk escalation or the FlagDanger regex
		// backstop (risk.go). Everything else runs straight through: no
		// dwell to measure, nothing to record beyond the run itself.
		approved := true
		if pre.Dangerous {
			t0 := time.Now()
			approved = s.Confirm(ConfirmRequest{
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
			ustep.SetDwell(time.Since(t0))
		}
		if !approved {
			s.RecordDecline(res, proposal.Command)
			return *res, nil
		}

		ec, err := s.Execute(ctx, res, ustep, proposal, pre, nil)
		if err != nil {
			return *res, err
		}
		post := s.JudgeResult(ctx, goal, ec.Command, ec.Result)
		ec.Usage.SetJudgePost(post.JudgeUsage, post.Attention, post.GoalAchieved)
		ec.Post = &post
	}
}
