// Package worktree checkpoints the human's own working directory so a
// rollback can offer to revert it. The container's snapshot stops at
// the bind mount, which is exactly where a goal does its real work.
//
// Everything runs through git plumbing against a scratch index, so the
// user's own index, branch and stash list are never touched. What git
// ignores, this ignores: build output is not state worth restoring.
package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Checkpoint is a git tree object naming one captured state.
type Checkpoint string

// Kind is what restoring a checkpoint would do to a path.
type Kind string

const (
	// Restored: the file changed or was deleted since the checkpoint,
	// and its old content comes back.
	Restored Kind = "restored"
	// Removed: the file did not exist at the checkpoint, so reverting
	// deletes it.
	Removed Kind = "removed"
)

// Change is one path a restore would touch, for showing a human before
// anything is written.
type Change struct {
	Path string
	Kind Kind
}

func (c Change) String() string { return string(c.Kind) + " " + c.Path }

// Available reports whether dir is inside a git work tree, which is
// what this package needs to checkpoint anything.
func Available(ctx context.Context, dir string) bool {
	out, err := run(ctx, dir, "", "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// Capture records dir's current contents, tracked and untracked alike,
// and returns the tree naming them.
func Capture(ctx context.Context, dir string) (Checkpoint, error) {
	index, cleanup, err := scratchIndex(dir)
	if err != nil {
		return "", err
	}
	defer cleanup()

	if _, err := run(ctx, dir, index, "add", "-A", "."); err != nil {
		return "", fmt.Errorf("worktree: stage: %w", err)
	}
	tree, err := run(ctx, dir, index, "write-tree")
	if err != nil {
		return "", fmt.Errorf("worktree: write-tree: %w", err)
	}
	return Checkpoint(strings.TrimSpace(tree)), nil
}

// Diff is what restoring to would change, relative to dir right now.
// Empty means the working directory already matches.
func Diff(ctx context.Context, dir string, to Checkpoint) ([]Change, error) {
	now, err := Capture(ctx, dir)
	if err != nil {
		return nil, err
	}
	if now == to {
		return nil, nil
	}
	// Reading old→new, an "A" is a path that arrived after the
	// checkpoint: reverting deletes it. Everything else comes back.
	out, err := run(ctx, dir, "", "diff", "--name-status", "-z", string(to), string(now))
	if err != nil {
		return nil, fmt.Errorf("worktree: diff: %w", err)
	}
	var changes []Change
	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		kind := Restored
		if strings.HasPrefix(fields[i], "A") {
			kind = Removed
		}
		changes = append(changes, Change{Path: fields[i+1], Kind: kind})
	}
	return changes, nil
}

// Restore puts dir back to the checkpoint: files that changed or were
// deleted come back, files created since are removed. Paths git
// ignores are left alone, having never been captured.
func Restore(ctx context.Context, dir string, to Checkpoint) error {
	changes, err := Diff(ctx, dir, to)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}

	index, cleanup, err := scratchIndex(dir)
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := run(ctx, dir, index, "read-tree", string(to)); err != nil {
		return fmt.Errorf("worktree: read-tree: %w", err)
	}

	var bring []string
	for _, c := range changes {
		if c.Kind == Restored {
			bring = append(bring, c.Path)
			continue
		}
		if err := os.Remove(filepath.Join(dir, c.Path)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("worktree: remove %s: %w", c.Path, err)
		}
	}
	if len(bring) > 0 {
		args := append([]string{"checkout-index", "-f", "--"}, bring...)
		if _, err := run(ctx, dir, index, args...); err != nil {
			return fmt.Errorf("worktree: checkout: %w", err)
		}
	}
	return nil
}

// scratchIndex is a throwaway index file, so staging never disturbs
// the one the human is using.
func scratchIndex(dir string) (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "detent-index-")
	if err != nil {
		return "", nil, fmt.Errorf("worktree: scratch index: %w", err)
	}
	name := f.Name()
	f.Close()
	// git wants to create it itself; an empty file is not a valid index.
	os.Remove(name)
	return name, func() { os.Remove(name) }, nil
}

func run(ctx context.Context, dir, index string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	if index != "" {
		cmd.Env = append(cmd.Env, "GIT_INDEX_FILE="+index)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
