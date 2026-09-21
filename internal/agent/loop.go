package agent

import (
	"context"
	"errors"
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
	if s.Runners == nil {
		return nil, fmt.Errorf("agent: no Runners wired")
	}
	s.append(propose.Message{Role: propose.RoleUser, Content: goal})
	prober := probe.New(probeRunner{runner: s.Runners.Probe()})
	if out := prober.Run(ctx, probe.Select(ctx, s.Judge, goal)); out != "" {
		s.append(propose.Message{Role: propose.RoleTool, Content: out})
	}
	// An abort during probe collection must close the goal here, not
	// fall through into the first Propose.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	res := &GoalResult{Goal: goal}
	res.Stats = s.Stats.StartGoal(goal)
	if id, ok, err := s.Snapshot(ctx); ok && err == nil {
		res.Baseline = id
	}
	res.BaselineTree = s.SnapshotWorktree(ctx)
	res.BaselineMark = len(s.Transcript)
	s.goalMark = res.BaselineMark
	return res, nil
}

// ProposeNext returns one proposal, its pre-confirm judgment, and what
// the propose call consumed.
func (s *Session) ProposeNext(ctx context.Context, goal string) (propose.Proposal, PreJudgment, usage.Usage, error) {
	if err := ctx.Err(); err != nil {
		return propose.Proposal{}, PreJudgment{}, usage.Usage{}, err
	}
	p, used, err := s.Proposer.Propose(ctx, s.Transcript)
	switch {
	case errors.Is(err, propose.ErrNotAProposal):
		// A model thrown by a failed command tends to answer in prose
		// about it. Ask once for the shape back rather than ending the
		// goal over one reply.
		p, used, err = s.reask(ctx, used,
			"That reply was prose, not a proposal. Reply with the JSON object only: "+
				"the one command to run next, or done with a summary.")
		if err != nil {
			return propose.Proposal{}, PreJudgment{}, used, fmt.Errorf("agent: %w", err)
		}
	case err != nil:
		return propose.Proposal{}, PreJudgment{}, used, fmt.Errorf("agent: %w", err)
	}
	if p.Done && len(s.Transcript) == s.goalMark {
		p, used, err = s.reask(ctx, used,
			"Nothing has run for this goal yet, so there is no output of its own to conclude from. "+
				"Earlier goals' output describes the past. Propose the command that answers this goal now.")
		if err != nil {
			return propose.Proposal{}, PreJudgment{}, used, err
		}
	}
	if p.Done {
		return p, PreJudgment{}, used, nil
	}
	return p, s.judgePre(ctx, goal, p.Command), used, nil
}

// reask puts one steer to the model and takes what comes back. The
// nudge is passed for this call only, never appended to the
// transcript, and asked once — a model that repeats itself is taken at
// its word rather than looped.
func (s *Session) reask(ctx context.Context, first usage.Usage, nudge string) (propose.Proposal, usage.Usage, error) {
	nudged := append(append([]propose.Message{}, s.Transcript...), propose.Message{
		Role: propose.RoleUser, Content: nudge,
	})
	p, again, err := s.Proposer.Propose(ctx, nudged)
	total := usage.Usage{
		PromptTokens:     first.PromptTokens + again.PromptTokens,
		CompletionTokens: first.CompletionTokens + again.CompletionTokens,
		Latency:          first.Latency + again.Latency,
		Model:            again.Model,
	}
	if err != nil {
		return propose.Proposal{}, total, fmt.Errorf("agent: %w", err)
	}
	return p, total, nil
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

// Execute runs an approved proposal, times it, and records the outcome.
// Post stays nil until the caller attaches JudgeResult. events may be nil.
func (s *Session) Execute(ctx context.Context, res *GoalResult, ustep *usage.Step, p propose.Proposal, pre PreJudgment, events chan<- host.StreamEvent) (*ExecutedCommand, error) {
	t0 := time.Now()
	runner, _ := s.Runners.Select(pre)
	outcome, err := runner.Run(ctx, p.Command, events)
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
	if pre.RunMode == RunModeSandbox {
		if id, ok, snapErr := s.Snapshot(ctx); ok && snapErr == nil {
			ec.SnapshotID = id
		}
		ec.Worktree = s.SnapshotWorktree(ctx)
	}
	res.Commands = append(res.Commands, ec)
	s.append(propose.Message{Role: propose.RoleAssistant,
		Content: propose.EncodeAssistantTurn(p)})
	s.append(propose.Message{Role: propose.RoleTool, Content: formatToolResult(p.Command, outcome)})
	ec.TranscriptMark = len(s.Transcript)
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
				RunMode:    pre.RunMode,
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
