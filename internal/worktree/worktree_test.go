package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAvailable(t *testing.T) {
	assert.True(t, Available(t.Context(), repo(t)))
	assert.False(t, Available(t.Context(), t.TempDir()), "a plain directory can't be checkpointed")
}

func TestHead_NamesTheCommitAndNothingOutsideGit(t *testing.T) {
	dir := repo(t)
	assert.Equal(t, strings.TrimSpace(git(t, dir, "rev-parse", "HEAD")), Head(t.Context(), dir))
	assert.Empty(t, Head(t.Context(), t.TempDir()))
}

// A modified file comes back, a deleted one comes back, and one created
// since the checkpoint is removed.
func TestRestore_UndoesWhatAGoalDid(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	d := open(t, dir)

	before, err := d.Capture(ctx)
	require.NoError(t, err)

	write(t, dir, "tracked.txt", "edited by the model\n")
	write(t, dir, "created.py", "print(1)\n")
	require.NoError(t, os.Remove(filepath.Join(dir, ".gitignore")))

	changes, err := d.Diff(ctx, before, "")
	require.NoError(t, err)
	assert.Equal(t, map[string]Kind{
		"tracked.txt": Restored, ".gitignore": Restored, "created.py": Removed,
	}, kinds(changes))

	require.NoError(t, d.RestoreTo(ctx, before, ""))
	assert.Equal(t, "original\n", read(t, dir, "tracked.txt"))
	assert.FileExists(t, filepath.Join(dir, ".gitignore"))
	assert.NoFileExists(t, filepath.Join(dir, "created.py"))
	assertClean(t, d, before)
}

// A rename is a deletion and an addition. Read as one record, it
// misaligned every path after it.
func TestRestore_UndoesARename(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	write(t, dir, "sub/a.txt", "same content, so git calls it a rename\n")
	commit(t, dir)
	d := open(t, dir)

	before, err := d.Capture(ctx)
	require.NoError(t, err)
	require.NoError(t, os.Rename(filepath.Join(dir, "sub/a.txt"), filepath.Join(dir, "sub/b.txt")))
	write(t, dir, "tracked.txt", "edited\n")

	changes, err := d.Diff(ctx, before, "")
	require.NoError(t, err)
	assert.Equal(t, map[string]Kind{
		"sub/a.txt": Restored, "sub/b.txt": Removed, "tracked.txt": Restored,
	}, kinds(changes))

	require.NoError(t, d.RestoreTo(ctx, before, ""))
	assert.FileExists(t, filepath.Join(dir, "sub/a.txt"))
	assert.NoFileExists(t, filepath.Join(dir, "sub/b.txt"))
	assert.Equal(t, "original\n", read(t, dir, "tracked.txt"))
}

// Undo puts back the exact bytes, whatever line endings the human's git
// converts: with autocrlf on, as Windows has it, LF must not come back CRLF.
func TestRestore_KeepsLineEndingsAsTheyWere(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	git(t, dir, "config", "core.autocrlf", "true")
	write(t, dir, ".gitattributes", "*.txt text\n")
	write(t, dir, "lf.txt", "one\ntwo\n")
	write(t, dir, "crlf.txt", "one\r\ntwo\r\n")
	d := open(t, dir)

	before, err := d.Capture(ctx)
	require.NoError(t, err)
	write(t, dir, "lf.txt", "changed\n")
	write(t, dir, "crlf.txt", "changed\r\n")
	require.NoError(t, d.RestoreTo(ctx, before, ""))

	assert.Equal(t, "one\ntwo\n", read(t, dir, "lf.txt"))
	assert.Equal(t, "one\r\ntwo\r\n", read(t, dir, "crlf.txt"))
}

// Committed under autocrlf, git stores LF while the file on disk is CRLF.
// The checkpoint must hold the disk's bytes, or undo turns the file LF.
func TestRestore_KeepsACommittedCRLFFileCRLF(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	userGit(t, dir, "config", "core.autocrlf", "true")
	write(t, dir, "crlf.txt", "one\r\ntwo\r\n")
	userGit(t, dir, "add", "crlf.txt")
	userGit(t, dir, "commit", "-qm", "crlf")
	require.Equal(t, "one\ntwo\n", userGit(t, dir, "cat-file", "-p", "HEAD:crlf.txt"), "git stored it LF")
	d := open(t, dir)

	before, err := d.Capture(ctx)
	require.NoError(t, err)
	write(t, dir, "crlf.txt", "changed\r\n")
	require.NoError(t, d.RestoreTo(ctx, before, ""))
	assert.Equal(t, "one\r\ntwo\r\n", read(t, dir, "crlf.txt"))
}

// A clean filter (git-lfs is one) stores something other than the file. Undo
// must bring back the file, not what git stored.
func TestRestore_KeepsAFilteredFileAsItWasOnDisk(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	userGit(t, dir, "config", "filter.up.clean", "tr a-z A-Z")
	userGit(t, dir, "config", "filter.up.smudge", "tr A-Z a-z")
	write(t, dir, ".gitattributes", "*.dat filter=up\n")
	write(t, dir, "x.dat", "hello\n")
	userGit(t, dir, "add", "-A")
	userGit(t, dir, "commit", "-qm", "filtered")
	require.Equal(t, "HELLO\n", userGit(t, dir, "cat-file", "-p", "HEAD:x.dat"), "git stored the cleaned form")
	d := open(t, dir)

	before, err := d.Capture(ctx)
	require.NoError(t, err)
	write(t, dir, "x.dat", "edited\n")
	require.NoError(t, d.RestoreTo(ctx, before, ""))
	assert.Equal(t, "hello\n", read(t, dir, "x.dat"))
}

// A committed file a .gitignore matches is still the human's, and a new index
// would skip it without being told it is tracked.
func TestRestore_CoversACommittedFileGitIgnores(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	write(t, dir, "keep.log", "committed\n")
	git(t, dir, "add", "-f", "keep.log")
	write(t, dir, ".gitignore", "ignored/\n*.log\n")
	commit(t, dir)
	d := open(t, dir)

	before, err := d.Capture(ctx)
	require.NoError(t, err)
	write(t, dir, "keep.log", "edited\n")
	require.NoError(t, d.RestoreTo(ctx, before, ""))
	assert.Equal(t, "committed\n", read(t, dir, "keep.log"))
}

func TestClose_RemovesTheIndex(t *testing.T) {
	d := open(t, repo(t))
	_, err := d.Capture(t.Context())
	require.NoError(t, err)
	require.FileExists(t, d.index)
	require.NoError(t, d.Close())
	assert.NoDirExists(t, filepath.Dir(d.index))
}

// detent's workspace is wherever it started, often below the repo root.
func TestRestore_FromASubdirectory(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	write(t, dir, "sub/a.txt", "a\n")
	commit(t, dir)
	sub := filepath.Join(dir, "sub")
	d := open(t, sub)

	before, err := d.Capture(ctx)
	require.NoError(t, err)
	write(t, dir, "sub/a.txt", "edited\n")
	write(t, dir, "sub/new/deep.txt", "new\n")
	write(t, dir, "tracked.txt", "outside the directory\n")

	changes, err := d.Diff(ctx, before, "")
	require.NoError(t, err)
	assert.Equal(t, map[string]Kind{"sub/a.txt": Restored, "sub/new/deep.txt": Removed}, kinds(changes),
		"only the directory it was opened on")

	require.NoError(t, d.RestoreTo(ctx, before, ""))
	assert.Equal(t, "a\n", read(t, dir, "sub/a.txt"))
	assert.NoDirExists(t, filepath.Join(sub, "new"), "a directory the request made goes with its files")
	assert.DirExists(t, sub, "but never the directory it was opened on")
	assert.Equal(t, "outside the directory\n", read(t, dir, "tracked.txt"))
}

func TestRestore_OddPaths(t *testing.T) {
	names := []string{"with space.txt", "dir with space/x.txt", "ünï.txt"}
	if runtime.GOOS != "windows" {
		names = append(names, "new\nline.txt", "tab\there.txt")
	}
	ctx := t.Context()
	dir := repo(t)
	for _, n := range names {
		write(t, dir, n, "before\n")
	}
	d := open(t, dir)
	before, err := d.Capture(ctx)
	require.NoError(t, err)

	for _, n := range names {
		write(t, dir, n, "after\n")
		write(t, dir, n+".new", "created\n")
	}
	changes, err := d.Diff(ctx, before, "")
	require.NoError(t, err)
	got := kinds(changes)
	for _, n := range names {
		assert.Equal(t, Restored, got[n], n)
		assert.Equal(t, Removed, got[n+".new"], n)
	}

	require.NoError(t, d.RestoreTo(ctx, before, ""))
	for _, n := range names {
		assert.Equal(t, "before\n", read(t, dir, n), n)
		assert.NoFileExists(t, filepath.Join(dir, n+".new"), n)
	}
}

// A file that became a directory, and the other way round, both come back.
func TestRestore_FileAndDirectorySwap(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	write(t, dir, "was-file", "file\n")
	write(t, dir, "was-dir/inner.txt", "inner\n")
	d := open(t, dir)
	before, err := d.Capture(ctx)
	require.NoError(t, err)

	require.NoError(t, os.Remove(filepath.Join(dir, "was-file")))
	write(t, dir, "was-file/now.txt", "now a dir\n")
	require.NoError(t, os.RemoveAll(filepath.Join(dir, "was-dir")))
	write(t, dir, "was-dir", "now a file\n")

	require.NoError(t, d.RestoreTo(ctx, before, ""))
	assert.Equal(t, "file\n", read(t, dir, "was-file"))
	assert.Equal(t, "inner\n", read(t, dir, "was-dir/inner.txt"))
	assertClean(t, d, before)
}

func TestRestore_Symlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	ctx := t.Context()
	dir := repo(t)
	require.NoError(t, os.Symlink("tracked.txt", filepath.Join(dir, "link")))
	d := open(t, dir)
	before, err := d.Capture(ctx)
	require.NoError(t, err)

	require.NoError(t, os.Remove(filepath.Join(dir, "link")))
	require.NoError(t, os.Symlink(".gitignore", filepath.Join(dir, "link")))
	require.NoError(t, os.Symlink("tracked.txt", filepath.Join(dir, "made")))

	require.NoError(t, d.RestoreTo(ctx, before, ""))
	target, err := os.Readlink(filepath.Join(dir, "link"))
	require.NoError(t, err)
	assert.Equal(t, "tracked.txt", target)
	_, err = os.Lstat(filepath.Join(dir, "made"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// What changed after detent last looked is the human's, so a revert
// leaves it and says so rather than throwing it away.
func TestRestore_LeavesUnseenChanges(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	d := open(t, dir)
	before, err := d.Capture(ctx)
	require.NoError(t, err)

	write(t, dir, "tracked.txt", "the request's edit\n")
	write(t, dir, "made.txt", "the request's file\n")
	seen, err := d.Capture(ctx)
	require.NoError(t, err)
	write(t, dir, "mine.txt", "the human's file\n")
	write(t, dir, "made.txt", "the human edited the request's file\n")

	changes, err := d.Diff(ctx, before, seen)
	require.NoError(t, err)
	unseen := map[string]bool{}
	for _, c := range changes {
		unseen[c.Path] = c.Unseen
	}
	assert.Equal(t, map[string]bool{"tracked.txt": false, "made.txt": true, "mine.txt": true}, unseen)

	err = d.RestoreTo(ctx, before, seen)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reverted 1 of 3")
	assert.Contains(t, err.Error(), "made.txt")
	assert.Equal(t, "original\n", read(t, dir, "tracked.txt"))
	assert.Equal(t, "the human's file\n", read(t, dir, "mine.txt"))
	assert.Equal(t, "the human edited the request's file\n", read(t, dir, "made.txt"))
}

func TestRestore_UnknownCheckpoint(t *testing.T) {
	d := open(t, repo(t))
	err := d.RestoreTo(t.Context(), "0123456789abcdef0123456789abcdef01234567", "")
	assert.ErrorIs(t, err, ErrGone)
}

// Untracked files are the normal case for a goal that writes something
// new, so a checkpoint must cover them.
func TestCapture_IncludesUntrackedFiles(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	write(t, dir, "notes.md", "# notes\n")
	d := open(t, dir)

	before, err := d.Capture(ctx)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, "notes.md")))

	require.NoError(t, d.RestoreTo(ctx, before, ""))
	assert.Equal(t, "# notes\n", read(t, dir, "notes.md"), "an untracked file is restorable")
}

// Build output is not worth reverting. What git ignores, this ignores,
// including the sandbox's own output directory, which ignores itself.
func TestCapture_SkipsIgnoredPaths(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	write(t, dir, "ignored/artifact.bin", "built\n")
	write(t, dir, ".detent-sandbox/.gitignore", "*\n")
	write(t, dir, ".detent-sandbox/call.out", "running\n")
	d := open(t, dir)

	before, err := d.Capture(ctx)
	require.NoError(t, err)
	write(t, dir, "ignored/artifact.bin", "rebuilt\n")
	write(t, dir, ".detent-sandbox/call.out", "done\n")
	write(t, dir, ".detent-sandbox/next.out", "next\n")

	changes, err := d.Diff(ctx, before, "")
	require.NoError(t, err)
	assert.Empty(t, changes, "an ignored file is neither captured nor reverted")

	require.NoError(t, d.RestoreTo(ctx, before, ""))
	assert.Equal(t, "rebuilt\n", read(t, dir, "ignored/artifact.bin"), "and is left alone")
	assert.Equal(t, "next\n", read(t, dir, ".detent-sandbox/next.out"))
}

// The whole point of the plumbing: checkpointing must be invisible to
// whatever the human has staged.
func TestCapture_LeavesTheUsersIndexAlone(t *testing.T) {
	dir := repo(t)
	write(t, dir, "staged.txt", "mine\n")
	git(t, dir, "add", "staged.txt")
	was := git(t, dir, "diff", "--cached", "--name-only")
	require.Contains(t, was, "staged.txt")

	_, err := open(t, dir).Capture(t.Context())
	require.NoError(t, err)
	assert.Equal(t, was, git(t, dir, "diff", "--cached", "--name-only"), "the user's index must be untouched")
}

// Started from a hook or a rebase, GIT_DIR names another repository.
func TestCapture_IgnoresInheritedGitEnvironment(t *testing.T) {
	dir := repo(t)
	other := repo(t)
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)

	d := open(t, dir)
	assert.Equal(t, dir, realpath(t, d.top))
}

// Mid-merge the human's index holds conflicts write-tree would refuse, and
// detent's own index never sees them.
func TestCapture_DuringAMergeConflict(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	git(t, dir, "checkout", "-q", "-b", "side")
	write(t, dir, "tracked.txt", "side\n")
	commit(t, dir)
	git(t, dir, "checkout", "-q", "-")
	write(t, dir, "tracked.txt", "main\n")
	commit(t, dir)
	cmd := exec.CommandContext(ctx, "git", "merge", "-q", "side")
	cmd.Dir = dir
	require.Error(t, cmd.Run(), "the merge conflicts")

	d := open(t, dir)
	before, err := d.Capture(ctx)
	require.NoError(t, err)
	write(t, dir, "tracked.txt", "resolved\n")
	require.NoError(t, d.RestoreTo(ctx, before, ""))
	assert.Contains(t, read(t, dir, "tracked.txt"), "<<<<<<<")
}

// Nothing changed means nothing to confirm and nothing to write.
func TestDiff_EmptyWhenUnchanged(t *testing.T) {
	ctx := t.Context()
	d := open(t, repo(t))
	c, err := d.Capture(ctx)
	require.NoError(t, err)
	assertClean(t, d, c)
}

func kinds(changes []Change) map[string]Kind {
	got := map[string]Kind{}
	for _, c := range changes {
		got[c.Path] = c.Kind
	}
	return got
}

func assertClean(t *testing.T, d *Dir, c Checkpoint) {
	t.Helper()
	after, err := d.Diff(t.Context(), c, "")
	require.NoError(t, err)
	assert.Empty(t, after, "nothing left to do once restored")
}

func open(t *testing.T, dir string) *Dir {
	t.Helper()
	d, err := Open(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// repo is a real git repository, because this package is a thin shell
// over git plumbing and a fake would prove nothing about it.
func repo(t *testing.T) string {
	t.Helper()
	dir := realpath(t, t.TempDir())
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "t@example.com")
	git(t, dir, "config", "user.name", "t")
	write(t, dir, "tracked.txt", "original\n")
	write(t, dir, ".gitignore", "ignored/\n")
	commit(t, dir)
	return dir
}

func realpath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return r
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	require.NoError(t, err)
	return string(b)
}

func commit(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "x")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = env("")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

// userGit runs git as the human would, attributes and filters on, unlike git.
func userGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}
