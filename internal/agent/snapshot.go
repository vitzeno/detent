package agent

import (
	"context"
	"fmt"
	"os"

	"github.com/vitzeno/detent/internal/worktree"
	"github.com/vitzeno/detent/logging"
)

// Snapshot checkpoints the sandbox environment. ok is false when no
// sandbox Runner is wired, or it doesn't support snapshotting.
func (s *Session) Snapshot(ctx context.Context) (SnapshotID, bool, error) {
	if s.Runners == nil {
		return "", false, nil
	}
	snap, ok := s.Runners.Sandbox().(Snapshotter)
	if !ok {
		return "", false, nil
	}
	id, err := snap.Snapshot(ctx)
	return id, true, err
}

// SnapshotWorktree records the human's own working directory, which
// the container snapshot never covers — the workspace is a bind mount
// from outside it. Empty when the directory isn't a git work tree;
// that's a normal configuration, not a failure.
func (s *Session) SnapshotWorktree(ctx context.Context) worktree.Checkpoint {
	dir, err := os.Getwd()
	if err != nil || !worktree.Available(ctx, dir) {
		return ""
	}
	tree, err := worktree.Capture(ctx, dir)
	if err != nil {
		return ""
	}
	return tree
}

// RollbackPlan is what rolling back to step would do, so a human can
// read it before anything is written. Files is empty when the working
// directory already matches the checkpoint, or isn't a git work tree.
type RollbackPlan struct {
	Step  int
	Files []worktree.Change
	tree  worktree.Checkpoint
}

// PlanRollback resolves step to the checkpoint it would restore and
// works out which of the human's own files that would touch. It writes
// nothing: Rollback does that, given the plan back.
func (s *Session) PlanRollback(ctx context.Context, res *GoalResult, step int) (RollbackPlan, error) {
	if step < 1 || step > len(res.Commands) {
		return RollbackPlan{}, fmt.Errorf("agent: rollback step %d out of range (1-%d)", step, len(res.Commands))
	}
	plan := RollbackPlan{Step: step, tree: res.BaselineTree}
	if step > 1 {
		plan.tree = res.Commands[step-2].Worktree
	}
	if plan.tree == "" {
		return plan, nil
	}
	dir, err := os.Getwd()
	if err != nil || !worktree.Available(ctx, dir) {
		return plan, nil
	}
	files, err := worktree.Diff(ctx, dir, plan.tree)
	if err != nil {
		// A diff we can't read is not a reason to block the container
		// rollback; it just means nothing is offered for the files.
		return plan, nil
	}
	plan.Files = markUnseen(ctx, dir, res, files)
	return plan, nil
}

// markUnseen flags paths that changed after detent's last checkpoint.
// Nothing it ran can account for those — they are the human's own
// edits, and reverting is the one thing here that can destroy work
// detent never made.
func markUnseen(ctx context.Context, dir string, res *GoalResult, files []worktree.Change) []worktree.Change {
	newest := res.BaselineTree
	if n := len(res.Commands); n > 0 && res.Commands[n-1].Worktree != "" {
		newest = res.Commands[n-1].Worktree
	}
	if newest == "" {
		return files
	}
	since, err := worktree.Diff(ctx, dir, newest)
	if err != nil {
		return files
	}
	unseen := make(map[string]bool, len(since))
	for _, c := range since {
		unseen[c.Path] = true
	}
	for i := range files {
		files[i].Unseen = unseen[files[i].Path]
	}
	return files
}

// Rollback undoes step (1-based) and every step after it, restoring
// the checkpoint taken before step ran and truncating res and
// Transcript to match. ok is false only when no sandbox Snapshotter is
// wired; an invalid step, or one whose predecessor left no checkpoint,
// is a real error rather than a silent no-op.
//
// revertFiles decides the human's own working directory: the container
// rolls back either way, but files under the workspace are only
// touched when asked, since they may hold edits detent never made.
func (s *Session) Rollback(ctx context.Context, res *GoalResult, step int, revertFiles bool) (bool, error) {
	logging.For(logging.Agent).InfoContext(ctx, "rolling back", logging.KeyEvent, logging.Rollback,
		"step", step, "revert_files", revertFiles, "steps_before", len(res.Commands))
	if s.Runners == nil {
		return false, nil
	}
	snap, ok := s.Runners.Sandbox().(Snapshotter)
	if !ok {
		return false, nil
	}
	plan, err := s.PlanRollback(ctx, res, step)
	if err != nil {
		return true, err
	}
	// Undoing step N means restoring the state it started from: the
	// checkpoint after N-1, or the goal's baseline when N is the first.
	target, mark := res.Baseline, res.BaselineMark
	if step > 1 {
		prev := res.Commands[step-2]
		target, mark = prev.SnapshotID, prev.TranscriptMark
	}
	if target == "" {
		return true, fmt.Errorf("agent: no checkpoint before step %d, nothing to roll back to", step)
	}
	// Checked before anything is restored: a compacted-away mark would
	// leave the transcript describing work the container no longer has.
	cut, ok := s.index(mark)
	if !ok {
		return true, fmt.Errorf("agent: step %d is older than the transcript kept in context, cannot roll back to it", step)
	}
	if err := snap.Rollback(ctx, target); err != nil {
		return true, err
	}
	if revertFiles && plan.tree != "" && len(plan.Files) > 0 {
		dir, err := os.Getwd()
		if err != nil {
			return true, fmt.Errorf("agent: rollback workspace: %w", err)
		}
		if err := worktree.Restore(ctx, dir, plan.tree); err != nil {
			return true, fmt.Errorf("agent: rollback workspace: %w", err)
		}
	}
	res.Commands = res.Commands[:step-1]
	s.Transcript = s.Transcript[:cut]
	s.seq = mark
	return true, nil
}
