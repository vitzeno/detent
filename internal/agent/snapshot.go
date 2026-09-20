package agent

import (
	"context"
	"fmt"
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

// Rollback undoes step (1-based) and every step after it, restoring
// the checkpoint taken before step ran and truncating res and
// Transcript to match. ok is false only when no sandbox Snapshotter is
// wired; an invalid step, or one whose predecessor left no checkpoint,
// is a real error rather than a silent no-op.
func (s *Session) Rollback(ctx context.Context, res *GoalResult, step int) (bool, error) {
	if s.Runners == nil {
		return false, nil
	}
	snap, ok := s.Runners.Sandbox().(Snapshotter)
	if !ok {
		return false, nil
	}
	if step < 1 || step > len(res.Commands) {
		return true, fmt.Errorf("agent: rollback step %d out of range (1-%d)", step, len(res.Commands))
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
	if err := snap.Rollback(ctx, target); err != nil {
		return true, err
	}
	res.Commands = res.Commands[:step-1]
	s.Transcript = s.Transcript[:mark]
	return true, nil
}
