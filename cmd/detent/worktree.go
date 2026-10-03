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
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !worktree.Available(ctx, dir) {
		return nil, nil
	}
	return worktree.Open(ctx, dir)
}
