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
	if req.RevertFiles && t.tree != "" {
		if w, has := e.worktree(); has {
			if err := w.Restore(ctx, t.tree); err != nil {
				e.notice("warn", "files were left as they are: "+err.Error())
			}
		}
	}
	var rewound bool
	e.trLock(func() { rewound = e.tr.truncate(t.mark) })
	if !rewound {
		e.notice("warn", "that request was compacted away, so the model still remembers a summary of it")
	}
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

func (e *Engine) worktree() (Worktreer, bool) {
	return e.worktreer, e.worktreer != nil
}
