package main

import (
	"context"
	"time"

	"github.com/vitzeno/detent/internal/worktree"
)

// openWorktree checkpoints the human's directory for undo, in the TUI and
// only inside a git work tree. The sandbox's workspace is the same directory.
func openWorktree(tui bool, dir string) (*worktree.Dir, error) {
	if !tui {
		return nil, nil //nolint:nilnil // headless has no undo, which is not an error
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !worktree.Available(ctx, dir) {
		return nil, nil //nolint:nilnil // outside a work tree there is nothing to checkpoint
	}
	return worktree.Open(ctx, dir)
}
