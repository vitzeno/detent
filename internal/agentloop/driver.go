package agentloop

import (
	"context"
	"fmt"
	"strings"

	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/shell"
)

// BeginGoal opens a goal; only RunGoal (not this) fails closed without Confirm.
func (s *Session) BeginGoal(goal string) (*GoalResult, error) {
	if strings.TrimSpace(goal) == "" {
		return nil, fmt.Errorf("agentloop: empty goal")
	}
	if s.Proposer == nil {
		return nil, fmt.Errorf("agentloop: no Proposer wired")
	}
	s.append(propose.Message{Role: propose.RoleUser, Content: goal})
	return &GoalResult{Goal: goal}, nil
}

// ProposeNext returns one proposal plus its pre-confirm judgment.
func (s *Session) ProposeNext(ctx context.Context, goal string) (propose.Proposal, PreJudgment, error) {
	if err := ctx.Err(); err != nil {
		return propose.Proposal{}, PreJudgment{}, err
	}
	p, err := s.Proposer.Propose(ctx, s.Transcript)
	if err != nil {
		return propose.Proposal{}, PreJudgment{}, fmt.Errorf("agentloop: %w", err)
	}
	if p.Done {
		return p, PreJudgment{}, nil
	}
	return p, s.judgePre(ctx, goal, p.Command), nil
}

func (s *Session) RecordDone(res *GoalResult, p propose.Proposal) {
	res.End = EndDone
	res.Summary = p.Summary
	s.append(propose.Message{Role: propose.RoleAssistant,
		Content: propose.EncodeAssistantTurn(p)})
	s.GoalsDone++
}

func (s *Session) RecordDecline(res *GoalResult, command string) {
	res.End = EndDeclined
	s.append(propose.Message{Role: propose.RoleUser, Content: fmt.Sprintf(
		"[goal ended by human: declined command `%s` after %d command(s)]",
		command, len(res.Commands))})
	s.GoalsDone++
}

func (s *Session) RecordBudget(res *GoalResult, budget int) {
	res.End = EndBudget
	s.append(propose.Message{Role: propose.RoleUser, Content: fmt.Sprintf(
		"[goal ended: step budget exhausted (%d/%d), goal not confirmed done]", budget, budget)})
	s.GoalsDone++
}

func (s *Session) RecordProposerError(res *GoalResult, err error) {
	res.End = EndProposerError
	s.append(propose.Message{Role: propose.RoleUser, Content: fmt.Sprintf(
		"[goal ended: proposer error: %v]", err)})
}

// Execute runs an approved proposal; Post stays nil until the caller attaches JudgeResult.
func (s *Session) Execute(ctx context.Context, res *GoalResult, p propose.Proposal, pre PreJudgment, onEvent func(shell.StreamEvent)) (*ExecutedCommand, error) {
	outcome, err := s.runFunc()(ctx, p.Command, onEvent)
	if err != nil {
		s.append(propose.Message{Role: propose.RoleAssistant,
			Content: propose.EncodeAssistantTurn(p)})
		s.append(propose.Message{Role: propose.RoleTool,
			Content: fmt.Sprintf("Command `%s` failed to execute: %v", p.Command, err)})
		res.Commands = append(res.Commands, ExecutedCommand{Command: p.Command, Pre: pre})
		if ctx.Err() == context.Canceled {
			res.End = EndAborted
		} else {
			res.End = EndProposerError
		}
		s.GoalsDone++
		return nil, fmt.Errorf("agentloop: run %q: %w", p.Command, err)
	}
	ec := ExecutedCommand{Command: p.Command, Result: outcome, Pre: pre}
	res.Commands = append(res.Commands, ec)
	s.append(propose.Message{Role: propose.RoleAssistant,
		Content: propose.EncodeAssistantTurn(p)})
	s.append(propose.Message{Role: propose.RoleTool, Content: formatToolResult(p.Command, outcome)})
	return &res.Commands[len(res.Commands)-1], nil
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
		return GoalResult{Goal: goal, End: EndConfirmMissing},
			fmt.Errorf("agentloop: no Confirm function wired, refusing to run")
	}
	res, err := s.BeginGoal(goal)
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

		proposal, pre, err := s.ProposeNext(ctx, goal)
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

		approved := s.Confirm(ConfirmRequest{
			Goal:       goal,
			Command:    proposal.Command,
			Rationale:  proposal.Rationale,
			Dangerous:  pre.Dangerous,
			RiskNote:   pre.RiskNote,
			Mutability: pre.Mutability,
			Step:       step,
			StepBudget: confirmBudget,
			History:    append([]ExecutedCommand(nil), res.Commands...),
			GoalsDone:  s.GoalsDone,
		})
		if !approved {
			s.RecordDecline(res, proposal.Command)
			return *res, nil
		}

		ec, err := s.Execute(ctx, res, proposal, pre, nil)
		if err != nil {
			return *res, err
		}
		post := s.JudgeResult(ctx, goal, ec.Command, ec.Result)
		ec.Post = &post
	}
}
