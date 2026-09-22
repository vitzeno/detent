package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/probe"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/usage"
	"github.com/vitzeno/detent/logging"
)

// probeRunner adapts a Runner to probe.Runner; probes never stream.
type probeRunner struct {
	runner Runner
}

// Run goes through runSafely for the reason Execute does, and because
// probes run in the background between goals, where a panicking
// Runner would take the session down unasked.
func (r probeRunner) Run(ctx context.Context, command string) (host.Result, error) {
	return runSafely(ctx, r.runner, command, nil)
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
	// A goal boundary is the only safe place to compact: mid-goal the
	// model needs its own steps intact, and this runs before the new
	// goal is appended so the fresh ask is never what gets dropped.
	log := logging.For(logging.Agent)
	ctx = logging.WithGoal(ctx, s.GoalsDone+1)
	log.InfoContext(ctx, "goal opened", logging.KeyEvent, logging.GoalBegin,
		"goal_text", goal, "transcript", len(s.Transcript))

	t0 := time.Now()
	s.compact(ctx)
	compacted := time.Since(t0)

	s.append(propose.Message{Role: propose.RoleUser, Content: goal})

	// Taken while the probes run: they are read-only, so a checkpoint
	// from before them is a checkpoint from before the goal.
	res := &GoalResult{Goal: goal}
	var baseline sync.WaitGroup
	baseline.Add(1)
	t3 := time.Now()
	var snapshotFor time.Duration
	go func() {
		defer baseline.Done()
		if id, ok, err := s.Snapshot(ctx); ok && err == nil {
			res.Baseline = id
		}
		res.BaselineTree = s.SnapshotWorktree(ctx)
		snapshotFor = time.Since(t3)
	}()

	t1 := time.Now()
	chosen := probe.Select(ctx, s.Judge, goal)
	chose := time.Since(t1)
	names := make([]string, 0, len(chosen))
	for _, pr := range chosen {
		names = append(names, pr.Name)
	}

	// Gathered as the last goal closed. A miss runs them now rather
	// than going without: no context is worse than a pause.
	t2 := time.Now()
	said, hit := s.take(ctx, chosen)
	if !hit {
		said = probe.New(probeRunner{runner: s.Runners.Probe()}).Each(ctx, chosen)
	}
	// Bounded like every other path in. One ps on a busy machine is
	// 200KB, twice the whole transcript budget, resent on every later
	// propose until compaction throws it away.
	if out := probe.Format(chosen, said); out != "" {
		s.append(propose.Message{Role: propose.RoleTool, Content: boundStr(out)})
	}
	// Timed per phase: these are serial and the first command waits on
	// all of them. Event gaps cannot be read as a chain, since judging
	// and view composition run alongside.
	log.InfoContext(ctx, "probes chosen", logging.KeyEvent, logging.ProbeRun, "probes", names,
		logging.KeyMS, ms(chose+time.Since(t2)), "select_ms", ms(chose),
		"run_ms", ms(time.Since(t2)), "compact_ms", ms(compacted), "prefetched", hit)
	// An abort during probe collection must close the goal here, not
	// fall through into the first Propose.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	baseline.Wait()
	res.Stats = s.Stats.StartGoal(goal)
	log.DebugContext(ctx, "goal ready to propose", logging.KeyEvent, logging.GoalReady,
		"snapshot_ms", ms(snapshotFor), logging.KeyMS, ms(time.Since(t0)))
	res.BaselineMark = s.mark()
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
	if p.Done && s.mark() == s.goalMark {
		p, used, err = s.reask(ctx, used,
			"Nothing has run for this goal yet, so there is no output of its own to conclude from. "+
				"Earlier goals' output describes the past. Propose the command that answers this goal now.")
		if err != nil {
			return propose.Proposal{}, PreJudgment{}, used, err
		}
	}
	log := logging.For(logging.Agent)
	if p.Done {
		log.InfoContext(ctx, "the model says the goal is met", logging.KeyEvent, logging.CmdPropose,
			"done", true, logging.KeyMS, used.Latency.Milliseconds())
		return p, PreJudgment{}, used, nil
	}
	pre := s.judgePre(ctx, goal, p.Command)
	log.InfoContext(ctx, "a command was proposed", logging.KeyEvent, logging.CmdPropose,
		"command", p.Command, "dangerous", pre.Dangerous,
		"mutability", pre.Mutability, "scope_risk", pre.ScopeRisk,
		"run_mode", pre.RunMode, logging.KeyReason, pre.RiskNote,
		"tokens", used.PromptTokens+used.CompletionTokens,
		logging.KeyMS, used.Latency.Milliseconds())
	return p, pre, used, nil
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
	s.steps++
	ctx = logging.WithStep(ctx, s.steps)
	log := logging.For(logging.Agent)
	log.InfoContext(ctx, "running", logging.KeyEvent, logging.CmdRun,
		"command", p.Command, "run_mode", pre.RunMode)

	t0 := time.Now()
	runner, _ := s.Runners.Select(pre)
	outcome, err := runSafely(ctx, runner, p.Command, events)
	elapsed := time.Since(t0)
	if err != nil {
		log.ErrorContext(ctx, "the command could not run", logging.KeyEvent, logging.CmdDone,
			"command", p.Command, logging.KeyReason, err.Error())
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
	log.InfoContext(ctx, "the command finished", logging.KeyEvent, logging.CmdDone,
		"exit", outcome.ExitCode, logging.KeyMS, elapsed.Milliseconds(),
		"bytes", len(outcome.Stdout)+len(outcome.Stderr),
		"stdout", logging.Body(outcome.Stdout), "stderr", logging.Body(outcome.Stderr))
	if pre.RunMode == RunModeSandbox {
		if id, ok, snapErr := s.Snapshot(ctx); ok && snapErr == nil {
			ec.SnapshotID = id
			log.InfoContext(ctx, "checkpointed", logging.KeyEvent, logging.Snapshot, "id", string(id))
		}
		ec.Worktree = s.SnapshotWorktree(ctx)
	}
	res.Commands = append(res.Commands, ec)
	s.append(propose.Message{Role: propose.RoleAssistant,
		Content: propose.EncodeAssistantTurn(p)})
	s.append(propose.Message{Role: propose.RoleTool, Content: formatToolResult(p.Command, outcome)})
	ec.TranscriptMark = s.mark()
	return ec, nil
}

// runSafely turns a panicking Runner into a failed step. Taking the
// session down would lose every checkpoint the human could still roll
// back to, which is worse than one broken command.
func runSafely(ctx context.Context, runner Runner, command string, events chan<- host.StreamEvent) (res host.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("runner panicked: %v", r)
		}
	}()
	return runner.Run(ctx, command, events)
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
	t0 := time.Now()
	post := s.judgePost(ctx, goal, command, resultView{
		ExitCode: result.ExitCode,
		Output:   out,
		Lines:    countLines(result.Stdout) + countLines(result.Stderr),
	})
	step.SetJudgePost(post.JudgeUsage, post.Attention, post.GoalAchieved)
	// The answer, not the question. Logging what was asked told a
	// reader nothing they could not see from cmd.run, and left the one
	// field that explains a loop, goal_achieved, unrecorded.
	logging.For(logging.Agent).DebugContext(ctx, "judged the result",
		logging.KeyEvent, logging.JudgePost, "command", command, "exit", result.ExitCode,
		"status", post.Status, "kind", post.RenderKind, "attention", post.Attention,
		"goal_achieved", post.GoalAchieved, "from_judge", post.FromJudge, logging.KeyMS, ms(time.Since(t0)))
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

// ms rounds a duration for a log field: microseconds on a network call
// are noise, and a whole number is what a query filters on.
func ms(d time.Duration) int64 { return d.Milliseconds() }
