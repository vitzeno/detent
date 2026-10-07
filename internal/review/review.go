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

// Patcher reads a diff between checkpoints, or a branch's against the one it
// left, satisfied by *worktree.Dir.
type Patcher interface {
	Patch(ctx context.Context, base, head worktree.Checkpoint, limit int) (string, bool, error)
	CaptureFiltered(ctx context.Context) (worktree.Checkpoint, error)
	MergeBase(ctx context.Context, ref string) (base, against string, err error)
}

// Watch answers LoadDiff from files, the same Dir the engine checkpoints with,
// since two over one directory would fight over its index, and records comments.
func Watch(ctx context.Context, bus *event.Bus, files Patcher) func() {
	kinds := event.Only(event.LoadDiffKind, event.CommentReviewKind, event.SubmitReviewKind)
	return bus.Handle(kinds, func(rec event.Record) {
		switch v := rec.Event.(type) {
		case event.LoadDiff:
			bus.Publish(load(ctx, files, v))
		case event.CommentReview:
			bus.Publish(event.ReviewCommented{Review: v.Review, Reviewed: v.Reviewed, Base: v.Base,
				Head: v.Head, Scope: v.Scope, Against: v.Against, Op: v.Op, Comment: v.Comment})
		case event.SubmitReview:
			// Closed before the prompt, so the review cannot be sent twice from what it starts.
			bus.Publish(event.ReviewSubmitted{Review: v.Review, Comments: len(v.Comments)})
			bus.Publish(event.SubmitPrompt{Text: prompt(v)})
		}
	})
}

func load(ctx context.Context, files Patcher, v event.LoadDiff) event.DiffLoaded {
	out := event.DiffLoaded{Base: v.Base, Head: v.Head, Branch: v.Branch}
	base, head := worktree.Checkpoint(v.Base), worktree.Checkpoint(v.Head)
	var err error
	if v.Branch {
		base, head, err = branch(ctx, files, v.Against, &out)
	}
	var patch string
	var cut bool
	if err == nil {
		patch, cut, err = files.Patch(ctx, base, head, maxPatchBytes)
	}
	switch {
	case errors.Is(err, worktree.ErrGone):
		out.Err = "git has pruned these files, so the changes can no longer be read"
	case errors.Is(err, worktree.ErrNoRepository):
		out.Err = "this directory is not a git repository, so it has no branch to review"
	case errors.Is(err, worktree.ErrNoBranch):
		out.Err = "there is no main or master branch to compare this one with: try /review branch <ref>"
	case err != nil:
		out.Err = err.Error()
	default:
		out.Files, out.Cut = parse(patch, cut), cut
	}
	return out
}

// branch is where the branch left against and the files as a commit would hold
// them, since a raw checkpoint against a commit shows every filtered file changed.
func branch(ctx context.Context, files Patcher, against string, out *event.DiffLoaded) (base, head worktree.Checkpoint, err error) {
	b, ref, err := files.MergeBase(ctx, against)
	if err != nil {
		return "", "", err
	}
	out.Base, out.Against = b, ref
	head, err = files.CaptureFiltered(ctx)
	return worktree.Checkpoint(b), head, err
}
