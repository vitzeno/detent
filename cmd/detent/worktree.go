package main

import (
	"context"
	"time"

	"github.com/vitzeno/detent/internal/worktree"
)

// headOf is the commit the human's directory is on, in every run, so a
// session can be replayed against the code it was asked about.
func headOf(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return worktree.Head(ctx, dir)
}

// openWorktree checkpoints the human's directory for undo and review, in the
// TUI, outside git too. The sandbox's workspace is the same directory.
func openWorktree(tui bool, dir string) (*worktree.Dir, error) {
	if !tui {
		return nil, nil //nolint:nilnil // headless has no undo, which is not an error
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !worktree.Available(ctx, dir) {
		return worktree.Private(ctx, dir, worktree.PrivateStore())
	}
	return worktree.Open(ctx, dir)
}
