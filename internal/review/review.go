// Package review answers what the review modal asks: the changes between two
// of the human's checkpoints, read with git and parsed once for ui, which
// cannot import worktree to read them itself.
package review

import (
	"context"
	"errors"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/worktree"
)

// maxPatchBytes bounds one diff, which travels the bus and sits in memory.
const maxPatchBytes = 4 << 20

// Patcher reads a diff between checkpoints, satisfied by *worktree.Dir.
type Patcher interface {
	Patch(ctx context.Context, base, head worktree.Checkpoint, limit int) (string, bool, error)
}

// Watch answers LoadDiff from files, the same Dir the engine checkpoints
// with, since two over one directory would fight over its index.
func Watch(ctx context.Context, bus *event.Bus, files Patcher) func() {
	return bus.Handle(event.Only(event.LoadDiffKind), func(rec event.Record) {
		if v, ok := rec.Event.(event.LoadDiff); ok {
			bus.Publish(load(ctx, files, v))
		}
	})
}

func load(ctx context.Context, files Patcher, v event.LoadDiff) event.DiffLoaded {
	out := event.DiffLoaded{Base: v.Base, Head: v.Head}
	patch, cut, err := files.Patch(ctx, worktree.Checkpoint(v.Base), worktree.Checkpoint(v.Head), maxPatchBytes)
	switch {
	case errors.Is(err, worktree.ErrGone):
		out.Err = "git has pruned these files, so the changes can no longer be read"
	case err != nil:
		out.Err = err.Error()
	default:
		out.Files, out.Cut = parse(patch, cut), cut
	}
	return out
}
