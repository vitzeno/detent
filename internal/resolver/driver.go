package resolver

import (
	"context"
	"time"

	"github.com/vitzeno/detent/internal/fileio"
	"github.com/vitzeno/detent/internal/usage"
	"github.com/vitzeno/detent/internal/worktree"
	"github.com/vitzeno/detent/ui"
)

func (r *Resolver) BeginGoal(ctx context.Context, goal string) (*ui.GoalResult, error) {
	res, err := r.sess.BeginGoal(ctx, goal)
	if err != nil {
		return nil, err
	}
	return toGoalResult(res), nil
}

func (r *Resolver) ProposeNext(ctx context.Context, goal string) (ui.Proposal, ui.PreJudgment, ui.Usage, error) {
	p, pre, used, err := r.sess.ProposeNext(ctx, goal)
	return toProposal(p), toPreJudgment(pre), toUsage(used), err
}

func (r *Resolver) RecordStep(res *ui.GoalResult, p ui.Proposal, pre ui.PreJudgment, proposeUsage ui.Usage, dwell time.Duration) ui.StepHandle {
	step := r.sess.RecordStep(goalRef(res), fromProposal(p), fromPreJudgment(pre), fromUsage(proposeUsage), dwell)
	return ui.StepHandle{Ref: step}
}

func (r *Resolver) Execute(ctx context.Context, res *ui.GoalResult, step ui.StepHandle, p ui.Proposal, pre ui.PreJudgment, events chan<- ui.StreamEvent) (*ui.ExecutedCommand, error) {
	agentRes := goalRef(res)
	ustep, _ := step.Ref.(*usage.Step)
	agentCh, done := relayEvents(ctx, events)
	aec, err := r.sess.Execute(ctx, agentRes, ustep, fromProposal(p), fromPreJudgment(pre), agentCh)
	<-done
	syncGoalResult(res, agentRes)
	if err != nil {
		return nil, err
	}
	// One DTO shared by res.Commands and the caller, not two copies, so
	// a later mutation (e.g. JudgeResult attaching Post) shows up either way.
	ec := toExecutedCommand(aec)
	res.Commands = append(res.Commands, ec)
	return ec, nil
}

func (r *Resolver) JudgeResult(ctx context.Context, goal, command string, result ui.Result, step ui.StepHandle) ui.PostJudgment {
	ustep, _ := step.Ref.(*usage.Step)
	post := r.sess.JudgeResult(ctx, goal, command, fromResult(result), ustep)
	return toPostJudgment(post)
}

func (r *Resolver) RecordDecline(res *ui.GoalResult, command string) {
	agentRes := goalRef(res)
	r.sess.RecordDecline(agentRes, command)
	syncGoalResult(res, agentRes)
}

func (r *Resolver) RecordDone(res *ui.GoalResult, p ui.Proposal) {
	agentRes := goalRef(res)
	r.sess.RecordDone(agentRes, fromProposal(p))
	syncGoalResult(res, agentRes)
}

func (r *Resolver) RecordProposerError(res *ui.GoalResult, err error) {
	agentRes := goalRef(res)
	r.sess.RecordProposerError(agentRes, err)
	syncGoalResult(res, agentRes)
}

// ReadFile wraps fileio.Read so ui never imports internal/fileio directly.
func (r *Resolver) ReadFile(path string) (content string, truncated bool, maxBytes int, err error) {
	content, truncated, err = fileio.Read(path)
	return content, truncated, fileio.MaxBytes, err
}

// SaveFile writes and records the change in one call, so a failed
// write never claims a change that didn't happen.
func (r *Resolver) SaveFile(path, diff, content string) error {
	if err := fileio.Write(path, content); err != nil {
		return err
	}
	r.sess.RecordFileSave(path, diff)
	return nil
}

func (r *Resolver) Rollback(ctx context.Context, res *ui.GoalResult, step int, revertFiles bool) (bool, error) {
	agentRes := goalRef(res)
	ok, err := r.sess.Rollback(ctx, agentRes, step, revertFiles)
	if !ok || err != nil {
		return ok, err
	}
	// Mirrors the same truncation onto the DTO the UI holds a pointer
	// to: step itself is undone, so it goes too.
	res.Commands = res.Commands[:step-1]
	syncGoalResult(res, agentRes)
	return true, nil
}

func (r *Resolver) RecordAbort(res *ui.GoalResult) {
	if res == nil {
		r.sess.RecordAbort(nil)
		return
	}
	agentRes := goalRef(res)
	r.sess.RecordAbort(agentRes)
	syncGoalResult(res, agentRes)
}

func (r *Resolver) Tracker() []ui.GoalStats {
	goals := r.sess.Tracker().Goals()
	out := make([]ui.GoalStats, len(goals))
	for i, g := range goals {
		out[i] = toGoalStats(g)
	}
	return out
}

// Reset forgets the conversation: transcript, goals and usage. The
// sandbox container is left alone.
func (r *Resolver) Reset() { r.sess.Reset() }

// PlanRollback reports which of the human's own files rolling back to
// step would touch, so the UI can ask before anything is written.
func (r *Resolver) PlanRollback(ctx context.Context, res *ui.GoalResult, step int) ([]ui.FileChange, error) {
	plan, err := r.sess.PlanRollback(ctx, goalRef(res), step)
	if err != nil {
		return nil, err
	}
	out := make([]ui.FileChange, len(plan.Files))
	for i, f := range plan.Files {
		out[i] = ui.FileChange{Path: f.Path, Removed: f.Kind == worktree.Removed}
	}
	return out, nil
}

func (r *Resolver) UsageSnapshot() ui.Snapshot {
	return toSnapshot(r.sess.Tracker().Snapshot())
}
