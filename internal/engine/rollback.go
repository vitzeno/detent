package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// maxUndoneFiles is how many files an undo's note names before counting the rest.
const maxUndoneFiles = 40

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
	// After RolledBack, so a replay truncates and then adds it, as this did.
	if note := e.undoneNote(ctx, t); note != "" {
		e.appended(e.root, uuid.Nil, uuid.Nil, func() []event.Message { return e.root.tr.note(note) })
	}
}

// undoneNote tells the model which files an undo left differing from before the
// request, since the transcript has forgotten it and would reason from code not there.
func (e *Engine) undoneNote(ctx context.Context, t *turnState) string {
	if !t.changed.Load() {
		return ""
	}
	w, has := e.worktree()
	if !has || t.tree == "" {
		return fmt.Sprintf("[Request %d was undone, but your files were not checkpointed, so whatever it "+
			"changed may still be on disk. Read a file again before relying on it.]", t.n)
	}
	paths, err := w.Changed(ctx, t.tree)
	switch {
	case err != nil:
		return fmt.Sprintf("[Request %d was undone, but which files still differ could not be read (%v). "+
			"Read a file again before relying on it.]", t.n, err)
	case len(paths) == 0:
		return ""
	}
	if len(paths) > maxUndoneFiles {
		paths = append(paths[:maxUndoneFiles], fmt.Sprintf("and %d more", len(paths)-maxUndoneFiles))
	}
	return fmt.Sprintf("[Request %d was undone, but these files still differ from before it, so read them "+
		"again before relying on them: %s]", t.n, strings.Join(paths, ", "))
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
