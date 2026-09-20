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

// Rollback restores the checkpoint after res.Commands[step-1]
// (1-based) and truncates res and Transcript to that point. ok is
// false only when no sandbox Snapshotter is wired; an invalid or
// non-sandboxed step is a real error, not a silent no-op.
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
	ec := res.Commands[step-1]
	if ec.SnapshotID == "" {
		return true, fmt.Errorf("agent: step %d did not run sandboxed, nothing to roll back to", step)
	}
	if err := snap.Rollback(ctx, ec.SnapshotID); err != nil {
		return true, err
	}
	res.Commands = res.Commands[:step]
	s.Transcript = s.Transcript[:ec.TranscriptMark]
	return true, nil
}
