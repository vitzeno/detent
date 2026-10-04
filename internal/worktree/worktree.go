// Package worktree checkpoints the human's working directory, which the
// container's snapshot never covers, so undo can offer to revert it. It uses git
// plumbing on an index of its own, never the human's, and skips what git ignores.
package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
)

// ErrGone is a checkpoint git no longer has, usually pruned by gc.
var ErrGone = errors.New("worktree: checkpoint no longer exists")

// emptyTree is git's well-known empty tree, which exists in every repository.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// keepEnv is the git environment that says how git is installed rather
// than which repository to act on.
var keepEnv = []string{"GIT_CONFIG_NOSYSTEM", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_EXEC_PATH"}

// Checkpoint is a git tree object naming one captured state. Nothing
// references it, so git gc prunes its blobs, two weeks by default.
type Checkpoint string

// Kind is what restoring a checkpoint would do to a path.
type Kind string

const (
	// Restored means the file changed or was deleted since the
	// checkpoint, and its old content comes back.
	Restored Kind = "restored"
	// Removed means the file did not exist at the checkpoint, so
	// reverting deletes it.
	Removed Kind = "removed"
)

// Change is one path a restore would touch, for showing a human before
// anything is written.
type Change struct {
	// Path is relative to the top of the work tree, as git prints it.
	Path string
	Kind Kind
	// Unseen marks a path changed after the last checkpoint, which nothing
	// detent ran accounts for, so reverting it throws work away.
	Unseen bool
}

// String reads as what a restore would do to the path.
func (c Change) String() string { return string(c.Kind) + " " + c.Path }

// Dir is one directory inside a git work tree, resolved once.
type Dir struct {
	top    string // the work tree's root, where every command runs
	prefix string // dir relative to top, "" at the root, else ending in /
	theirs string // the human's index, read once for tracked files git ignores

	// index is detent's own, kept for its stat cache. Never seeded from theirs,
	// whose cleaned blobs a restore with filters off would write over their files.
	mu    sync.Mutex
	index string
}

// Available reports whether dir is inside a git work tree, which is
// what this package needs to checkpoint anything.
func Available(ctx context.Context, dir string) bool {
	out, err := run(ctx, dir, "", nil, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// Open resolves dir's work tree, so a directory below the root checkpoints
// only itself and every path git prints is joined onto the right place.
func Open(ctx context.Context, dir string) (*Dir, error) {
	ask := func(dir string, arg ...string) (string, error) {
		out, err := run(ctx, dir, "", nil, append([]string{"rev-parse"}, arg...)...)
		return strings.TrimSuffix(out, "\n"), err
	}
	top, err := ask(dir, "--show-toplevel")
	if err != nil || top == "" {
		return nil, fmt.Errorf("worktree: %s is not inside a work tree: %w", dir, err)
	}
	prefix, err := ask(dir, "--show-prefix")
	if err != nil {
		return nil, fmt.Errorf("worktree: %w", err)
	}
	theirs, err := ask(top, "--git-path", "index")
	if err != nil {
		return nil, fmt.Errorf("worktree: %w", err)
	}
	if !filepath.IsAbs(theirs) {
		theirs = filepath.Join(top, theirs)
	}
	scratch, err := os.MkdirTemp("", "detent-index-")
	if err != nil {
		return nil, fmt.Errorf("worktree: index: %w", err)
	}
	return &Dir{top: top, prefix: prefix, theirs: theirs, index: filepath.Join(scratch, "index")}, nil
}

// Close removes detent's index. The checkpoints stay in the repository.
func (d *Dir) Close() error { return os.RemoveAll(filepath.Dir(d.index)) }

// Capture records the directory's current contents, tracked and
// untracked alike, and returns the tree naming them.
func (d *Dir) Capture(ctx context.Context) (Checkpoint, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, err := d.capture(ctx)
	if err != nil && ctx.Err() == nil {
		// A broken index is built again from nothing, which costs one full hash.
		_ = os.Remove(d.index)
		c, err = d.capture(ctx)
	}
	return c, err
}

// Diff is what restoring to would change, empty when nothing would. A path
// changed since seen is marked Unseen, and an empty seen marks nothing.
func (d *Dir) Diff(ctx context.Context, to, seen Checkpoint) ([]Change, error) {
	if err := d.exists(ctx, to); err != nil {
		return nil, err
	}
	now, err := d.Capture(ctx)
	if err != nil {
		return nil, err
	}
	if now == to {
		return nil, nil
	}
	changes, err := d.changes(ctx, to, now)
	if err != nil {
		return nil, err
	}
	if seen == "" || seen == now {
		return changes, nil
	}
	if err := d.exists(ctx, seen); err != nil {
		return nil, err
	}
	since, err := d.changes(ctx, seen, now)
	if err != nil {
		return nil, err
	}
	touched := map[string]bool{}
	for _, c := range since {
		touched[c.Path] = true
	}
	for i := range changes {
		changes[i].Unseen = touched[changes[i].Path]
	}
	return changes, nil
}

// RestoreTo brings back changed and deleted files and removes new ones.
// Ignored paths stay, and so does anything changed since seen, which the error names.
func (d *Dir) RestoreTo(ctx context.Context, to, seen Checkpoint) error {
	changes, err := d.Diff(ctx, to, seen)
	if err != nil {
		return err
	}
	var bring, drop, kept []string
	for _, c := range changes {
		switch {
		case c.Unseen:
			kept = append(kept, c.Path)
		case c.Kind == Restored:
			bring = append(bring, c.Path)
		default:
			drop = append(drop, c.Path)
		}
	}

	// Restored first, and nothing is deleted unless that worked.
	var errs []error
	done := 0
	if err := d.checkout(ctx, to, bring); err != nil {
		errs = append(errs, err, fmt.Errorf("removed none of %d new file(s)", len(drop)))
	} else {
		n, err := d.remove(drop, bring)
		done += len(bring) + n
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(kept) > 0 {
		errs = append(errs, fmt.Errorf("left %d file(s) changed after the last request: %s",
			len(kept), strings.Join(kept, ", ")))
	}
	if len(errs) > 0 {
		return fmt.Errorf("worktree: reverted %d of %d file(s): %w", done, len(changes), errors.Join(errs...))
	}
	return nil
}

// Checkpoint captures the directory for the engine, which keeps only the id.
func (d *Dir) Checkpoint(ctx context.Context) (string, error) {
	c, err := d.Capture(ctx)
	return string(c), err
}

// Restore reverts to id for the engine, leaving alone whatever changed
// since seen, which is how the directory stood when detent last looked.
func (d *Dir) Restore(ctx context.Context, id, seen string) error {
	return d.RestoreTo(ctx, Checkpoint(id), Checkpoint(seen))
}

func (d *Dir) capture(ctx context.Context) (Checkpoint, error) {
	if _, err := os.Stat(d.index); errors.Is(err, os.ErrNotExist) {
		if err := d.addIgnoredTracked(ctx); err != nil {
			return "", err
		}
	}
	if _, err := d.git(ctx, d.index, nil, "add", "-A", "--", d.pathspec()); err != nil {
		return "", fmt.Errorf("worktree: stage: %w", err)
	}
	tree, err := d.git(ctx, d.index, nil, "write-tree")
	if err != nil {
		return "", fmt.Errorf("worktree: write-tree: %w", err)
	}
	return Checkpoint(strings.TrimSpace(tree)), nil
}

// addIgnoredTracked stages the committed files a .gitignore matches, which
// add -A skips in a new index. Once there, add -A keeps them up to date.
func (d *Dir) addIgnoredTracked(ctx context.Context) error {
	paths, err := d.git(ctx, d.theirs, nil, "ls-files", "-z", "--cached", "--ignored",
		"--exclude-standard", "--", d.pathspec())
	if err != nil || paths == "" {
		// No index of theirs, or nothing in it git ignores.
		return nil
	}
	if _, err := d.git(ctx, d.index, strings.NewReader(paths), "add", "-f",
		"--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
		return fmt.Errorf("worktree: stage ignored tracked files: %w", err)
	}
	return nil
}

// changes reads from→to: an "A" is a path that arrived after from, so
// reverting deletes it, and everything else comes back.
func (d *Dir) changes(ctx context.Context, from, to Checkpoint) ([]Change, error) {
	// Plumbing ignores diff.renames, and --no-renames keeps every record a pair.
	out, err := d.git(ctx, "", nil, "diff-tree", "-r", "-z", "--no-renames", "--name-status",
		string(from), string(to), "--", d.pathspec())
	if err != nil {
		return nil, fmt.Errorf("worktree: diff: %w", err)
	}
	if out == "" {
		return nil, nil
	}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("worktree: diff: unexpected output %q", out)
	}
	var changes []Change
	for i := 0; i < len(fields); i += 2 {
		kind := Restored
		if fields[i] == "A" {
			kind = Removed
		}
		changes = append(changes, Change{Path: fields[i+1], Kind: kind})
	}
	return changes, nil
}

// checkout writes paths from the tree, over whatever stands there now.
func (d *Dir) checkout(ctx context.Context, to Checkpoint, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	index, cleanup, err := scratchIndex()
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := d.git(ctx, index, nil, "read-tree", string(to)); err != nil {
		return fmt.Errorf("read-tree: %w", err)
	}
	// Paths go on stdin, since a large revert would overflow argv.
	stdin := strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	if _, err := d.git(ctx, index, stdin, "checkout-index", "-f", "-z", "--stdin"); err != nil {
		return fmt.Errorf("checkout: %w", err)
	}
	return nil
}

// remove deletes paths created since the checkpoint, then any directory
// that leaves empty. A path that is now a directory holding restored files stays.
func (d *Dir) remove(paths, restored []string) (int, error) {
	var errs []error
	done := 0
	for _, p := range paths {
		if slices.ContainsFunc(restored, func(r string) bool { return strings.HasPrefix(r, p+"/") }) {
			done++
			continue
		}
		full := filepath.Join(d.top, filepath.FromSlash(p))
		err := os.Remove(full)
		switch {
		case err == nil:
			d.pruneEmpty(filepath.Dir(full))
		case !gone(err):
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
			continue
		}
		done++
	}
	return done, errors.Join(errs...)
}

// pruneEmpty removes dir and its parents while they are empty, stopping
// at the directory this was opened on.
func (d *Dir) pruneEmpty(dir string) {
	root := filepath.Join(d.top, filepath.FromSlash(d.prefix))
	for dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)) {
		if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() || os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// exists maps a pruned checkpoint to ErrGone rather than git's own words.
func (d *Dir) exists(ctx context.Context, c Checkpoint) error {
	if _, err := d.git(ctx, "", nil, "cat-file", "-e", string(c)+"^{tree}"); err != nil {
		return fmt.Errorf("%w: %s", ErrGone, c)
	}
	return nil
}

// pathspec scopes a command to the directory, read literally.
func (d *Dir) pathspec() string {
	if d.prefix == "" {
		return "."
	}
	return ":(literal)" + d.prefix
}

func (d *Dir) git(ctx context.Context, index string, stdin io.Reader, args ...string) (string, error) {
	return run(ctx, d.top, index, stdin, args...)
}

func run(ctx context.Context, dir, index string, stdin io.Reader, args ...string) (string, error) {
	// Hooks off, and no line ending conversion, so a checkpoint holds the exact bytes.
	full := append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.autocrlf=false",
		"-c", "core.attributesFile=" + os.DevNull}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = env(index)
	cmd.Stdin = stdin
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// env drops inherited GIT_* variables, so a detent started from a hook or
// a rebase still checkpoints the directory it was asked to.
func env(index string) []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "GIT_") && !slices.Contains(keepEnv, name) {
			continue
		}
		out = append(out, kv)
	}
	// The empty tree as the attribute source, and no system attributes: no
	// text, eol or filter rule (git-lfs included) rewrites what is kept.
	out = append(out, "GIT_OPTIONAL_LOCKS=0", "GIT_ATTR_SOURCE="+emptyTree, "GIT_ATTR_NOSYSTEM=1")
	if index != "" {
		out = append(out, "GIT_INDEX_FILE="+index)
	}
	return out
}

// scratchIndex is a throwaway index in its own directory, so staging
// never disturbs the one the human is using and a stale lock goes with it.
func scratchIndex() (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "detent-index-")
	if err != nil {
		return "", nil, fmt.Errorf("worktree: scratch index: %w", err)
	}
	return filepath.Join(dir, "index"), func() { _ = os.RemoveAll(dir) }, nil
}

// gone is a path already absent, or one whose parent is now a file.
func gone(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}
