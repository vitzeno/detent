package engine

import (
	"context"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// rollback restores a Turn's checkpoint and rewinds the transcript to
// where that Turn began. Only called while idle.
func (e *Engine) rollback(ctx context.Context, req event.RequestRollback) {
	t, ok := e.past[req.Turn]
	if !ok {
		e.notice("error", "no such request to undo")
		return
	}
	// Refused before anything is restored, so a refusal leaves nothing half undone.
	var reachable bool
	e.root.lock(func() { reachable = e.root.tr.reaches(t.mark) })
	if !reachable {
		e.notice("error", "that request was compacted into a summary of earlier work, so it can no longer be undone")
		return
	}
	if t.snap != "" {
		s, has := e.snapshotter()
		if !has {
			e.notice("error", "nothing is checkpointed, so there is nothing to undo")
			return
		}
		if err := s.Rollback(ctx, t.snap); err != nil {
			e.notice("error", "could not undo: "+err.Error())
			return
		}
	} else if _, has := e.snapshotter(); has {
		e.notice("warn", "this request had no checkpoint, so the sandbox keeps what it did")
	}
	if req.RevertFiles {
		e.revertFiles(ctx, t)
	}
	e.root.lock(func() { e.root.tr.truncate(t.mark) })
	e.forgetFrom(req.Turn)
	e.bus.Publish(event.RolledBack{Turn: req.Turn, RevertFiles: req.RevertFiles})
}

// forgetFrom drops the rolled-back Turn and everything after it: they
// no longer happened, so they are no longer targets.
func (e *Engine) forgetFrom(id uuid.UUID) {
	from, ok := e.past[id]
	if !ok {
		return
	}
	for tid, t := range e.past {
		if t.n >= from.n {
			delete(e.past, tid)
		}
	}
	e.turns = from.n - 1
}

// revertFiles puts the human's own directory back. What changed after the
// last Turn ended is theirs, so it stays and the notice names it.
func (e *Engine) revertFiles(ctx context.Context, t *turnState) {
	w, has := e.worktree()
	if !has || t.tree == "" {
		e.notice("warn", "your files were not checkpointed for this request, so they stay as they are")
		return
	}
	if err := w.Restore(ctx, t.tree, e.settled); err != nil {
		e.notice("warn", "your files were not all reverted: "+err.Error())
	}
	e.settled = t.tree
}

func (e *Engine) worktree() (Worktreer, bool) {
	return e.worktreer, e.worktreer != nil
}

func (e *Engine) snapshotter() (Snapshotter, bool) {
	r, _ := e.runners.Select(event.UnknownRisk())
	s, ok := r.(Snapshotter)
	return s, ok
}
