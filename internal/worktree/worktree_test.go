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

// A directory the request made where a file was, holding a file the human added
// since, stays a directory. Restoring the file over it deleted their work.
func TestRestore_KeepsTheHumansFileInADirectoryTheRequestMade(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	write(t, dir, "foo", "file\n")
	d := open(t, dir)
	before, err := d.Capture(ctx)
	require.NoError(t, err)

	require.NoError(t, os.Remove(filepath.Join(dir, "foo")))
	write(t, dir, "foo/a", "the request's\n")
	seen, err := d.Capture(ctx)
	require.NoError(t, err)
	write(t, dir, "foo/b", "the human's\n")

	err = d.RestoreTo(ctx, before, seen)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "foo/b")
	assert.Equal(t, "the human's\n", read(t, dir, "foo/b"))
}

// A request that clears .gitignore leaves what it ignored looking new. Undo
// reads the old rules, so the human's ignored files are not deleted as the request's.
func TestRestore_KeepsWhatTheOldGitignoreIgnored(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	write(t, dir, "ignored/secret.env", "KEY=1\n")
	d := open(t, dir)
	before, err := d.Capture(ctx)
	require.NoError(t, err)

	write(t, dir, ".gitignore", "")
	write(t, dir, "made.txt", "the request's\n")

	changes, err := d.Diff(ctx, before, "")
	require.NoError(t, err)
	assert.Equal(t, map[string]Kind{".gitignore": Restored, "made.txt": Removed}, kinds(changes))

	require.NoError(t, d.RestoreTo(ctx, before, ""))
	assert.Equal(t, "KEY=1\n", read(t, dir, "ignored/secret.env"))
	assert.Equal(t, "ignored/\n", read(t, dir, ".gitignore"))
	assert.NoFileExists(t, filepath.Join(dir, "made.txt"))
}

// A repository inside this one is its own, so a checkpoint leaves it out. One
// with no commit failed every capture, and one with a commit "reverted" nothing.
func TestCapture_LeavesANestedRepositoryOut(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	git(t, dir, "init", "-q", "fresh")
	write(t, dir, "fresh/f", "in a repository with no commit\n")
	nested(t, dir, "held")
	nested(t, dir, "sub")
	git(t, dir, "add", "sub")
	git(t, dir, "commit", "-qm", "a submodule")
	d := open(t, dir)
	before, err := d.Capture(ctx)
	require.NoError(t, err)

	for _, r := range []string{"held", "sub"} {
		write(t, dir, r+"/f", "edited\n")
		git(t, filepath.Join(dir, r), "commit", "-qam", "y")
	}
	write(t, dir, "tracked.txt", "edited\n")

	changes, err := d.Diff(ctx, before, "")
	require.NoError(t, err)
	assert.Equal(t, map[string]Kind{"tracked.txt": Restored}, kinds(changes))
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

// Patch reads what changed between two checkpoints, a new file included.
func TestPatch_ReadsTheChangeBetweenTwoCheckpoints(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	d := open(t, dir)
	before, err := d.Capture(ctx)
	require.NoError(t, err)
	write(t, dir, "tracked.txt", "edited\n")
	write(t, dir, "made.txt", "new\n")

	patch, cut, err := d.Patch(ctx, before, "", 1<<20)
	require.NoError(t, err)
	assert.False(t, cut)
	assert.Contains(t, patch, "diff --git a/made.txt b/made.txt\nnew file mode")
	assert.Contains(t, patch, "-original\n+edited\n")
}

// A CRLF file committed under autocrlf is stored LF. Checkpoint to checkpoint,
// one changed line is one changed line, not the whole file.
func TestPatch_ShowsOnlyTheLineThatChanged(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	userGit(t, dir, "config", "core.autocrlf", "true")
	write(t, dir, "crlf.txt", "one\r\ntwo\r\nthree\r\n")
	userGit(t, dir, "add", "crlf.txt")
	userGit(t, dir, "commit", "-qm", "crlf")
	d := open(t, dir)
	before, err := d.Capture(ctx)
	require.NoError(t, err)
	write(t, dir, "crlf.txt", "one\r\nTWO\r\nthree\r\n")

	patch, _, err := d.Patch(ctx, before, "", 1<<20)
	require.NoError(t, err)
	assert.Contains(t, patch, "-two\r\n+TWO\r\n")
	assert.NotContains(t, patch, "-one")
}

// git reads a blob from the file when the index says they match. With filters
// off that file is not what the blob holds, and the change vanished from the patch.
func TestPatch_ReadsWhatTheTreesHoldNotTheFiles(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	userGit(t, dir, "config", "filter.up.clean", "tr a-z A-Z")
	userGit(t, dir, "config", "filter.up.smudge", "tr A-Z a-z")
	write(t, dir, ".gitattributes", "*.dat filter=up\n")
	write(t, dir, "x.dat", "hello\n")
	userGit(t, dir, "add", "-A")
	userGit(t, dir, "commit", "-qm", "filtered")
	d := open(t, dir)

	patch, _, err := d.Patch(ctx, Checkpoint(strings.TrimSpace(git(t, dir, "rev-parse", "HEAD^{tree}"))), "", 1<<20)
	require.NoError(t, err)
	assert.Contains(t, patch, "-HELLO\n+hello\n", "the commit holds HELLO and the raw capture hello")
}

// A diff past the limit ends at a whole line within it, and says it was cut.
func TestPatch_CutsAtAWholeLine(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	d := open(t, dir)
	before, err := d.Capture(ctx)
	require.NoError(t, err)
	write(t, dir, "big.txt", strings.Repeat("a line of text\n", 10000))

	patch, cut, err := d.Patch(ctx, before, "", 1000)
	require.NoError(t, err)
	assert.True(t, cut)
	assert.LessOrEqual(t, len(patch), 1000)
	assert.True(t, strings.HasSuffix(patch, "\n"), "never half a line")
}

// A checkpoint git has pruned is ErrGone, as for a restore.
func TestPatch_UnknownCheckpoint(t *testing.T) {
	d := open(t, repo(t))
	_, _, err := d.Patch(t.Context(), "0123456789abcdef0123456789abcdef01234567", "", 1<<20)
	assert.ErrorIs(t, err, ErrGone)
}

// Under autocrlf a commit holds LF where the disk holds CRLF, so captured as a
// commit would hold it an untouched repository is exactly HEAD, unlike a raw capture.
func TestCaptureFiltered_HoldsWhatACommitWould(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	userGit(t, dir, "config", "core.autocrlf", "true")
	write(t, dir, "crlf.txt", "one\r\ntwo\r\n")
	userGit(t, dir, "add", "crlf.txt")
	userGit(t, dir, "commit", "-qm", "crlf")
	head := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD^{tree}"))
	d := open(t, dir)

	raw, err := d.Capture(ctx)
	require.NoError(t, err)
	require.NotEqual(t, head, string(raw), "the raw capture keeps the disk's CRLF")
	filtered, err := d.CaptureFiltered(ctx)
	require.NoError(t, err)
	assert.Equal(t, head, string(filtered))

	write(t, dir, "made.txt", "new\n")
	write(t, dir, "crlf.txt", "one\r\nTWO\r\n")
	filtered, err = d.CaptureFiltered(ctx)
	require.NoError(t, err)
	assert.Equal(t, "crlf.txt\nmade.txt", strings.TrimSpace(git(t, dir, "diff-tree", "-r", "--name-only", head, string(filtered))))
	assert.Equal(t, "one\nTWO\n", git(t, dir, "cat-file", "-p", string(filtered)+":crlf.txt"),
		"an edited file is stored as a commit would store it")
}

// The human's index is copied, never written.
func TestCaptureFiltered_LeavesTheUsersIndexAlone(t *testing.T) {
	dir := repo(t)
	write(t, dir, "made.txt", "new\n")
	before, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	require.NoError(t, err)
	_, err = open(t, dir).CaptureFiltered(t.Context())
	require.NoError(t, err)
	after, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.Contains(t, userGit(t, dir, "status", "--porcelain"), "?? made.txt", "still untracked")
}

// A branch is compared from where it left main, which a later commit to
// main does not move.
func TestMergeBase_FindsWhereTheBranchLeftMain(t *testing.T) {
	ctx := t.Context()
	dir := repo(t)
	git(t, dir, "branch", "-M", "main")
	start := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
	git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "f.txt", "f\n")
	commit(t, dir)
	git(t, dir, "checkout", "-q", "main")
	write(t, dir, "m.txt", "m\n")
	commit(t, dir)
	git(t, dir, "checkout", "-q", "feature")

	base, against, err := open(t, dir).MergeBase(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, start, base)
	assert.Equal(t, "main", against)
}

func TestMergeBase_SaysWhenThereIsNoMain(t *testing.T) {
	dir := repo(t)
	git(t, dir, "branch", "-M", "trunk")
	_, _, err := open(t, dir).MergeBase(t.Context(), "")
	assert.ErrorIs(t, err, ErrNoBranch)
}

// Outside git a directory keeps its checkpoints in a git directory of detent's
// own, so undo works there too and nothing is written into it.
func TestPrivate_UndoesWhatARequestDidOutsideGit(t *testing.T) {
	ctx := t.Context()
	dir := plain(t)
	write(t, dir, "notes.txt", "mine\n")
	write(t, dir, "sub/keep.txt", "kept\n")
	d := private(t, dir)

	before, err := d.Capture(ctx)
	require.NoError(t, err)
	write(t, dir, "notes.txt", "the request's\n")
	require.NoError(t, os.Remove(filepath.Join(dir, "sub/keep.txt")))
	write(t, dir, "made.txt", "new\n")

	changes, err := d.Diff(ctx, before, "")
	require.NoError(t, err)
	assert.Equal(t, map[string]Kind{"notes.txt": Restored, "sub/keep.txt": Restored, "made.txt": Removed}, kinds(changes))
	require.NoError(t, d.RestoreTo(ctx, before, ""))
	assert.Equal(t, "mine\n", read(t, dir, "notes.txt"))
	assert.Equal(t, "kept\n", read(t, dir, "sub/keep.txt"))
	assert.NoFileExists(t, filepath.Join(dir, "made.txt"))
	assert.NoDirExists(t, filepath.Join(dir, ".git"), "the directory was left as it was")
	assert.False(t, Available(ctx, dir), "and is still not a repository")
}

// Review reads the same checkpoints, but a branch needs a repository.
func TestPrivate_ReviewsAChangeButNoBranch(t *testing.T) {
	ctx := t.Context()
	dir := plain(t)
	write(t, dir, "a.txt", "one\n")
	d := private(t, dir)
	before, err := d.Capture(ctx)
	require.NoError(t, err)
	write(t, dir, "a.txt", "two\n")

	patch, _, err := d.Patch(ctx, before, "", 1<<20)
	require.NoError(t, err)
	assert.Contains(t, patch, "+two")
	_, _, err = d.MergeBase(ctx, "")
	require.ErrorIs(t, err, ErrNoRepository)
}

// What nobody would undo, and what would make every capture slow, is left out.
func TestPrivate_LeavesOutDependenciesAndCaches(t *testing.T) {
	ctx := t.Context()
	dir := plain(t)
	d := private(t, dir)
	before, err := d.Capture(ctx)
	require.NoError(t, err)
	write(t, dir, "node_modules/x/index.js", "dep\n")
	write(t, dir, ".venv/bin/python", "env\n")
	write(t, dir, "app.py", "code\n")

	changes, err := d.Diff(ctx, before, "")
	require.NoError(t, err)
	assert.Equal(t, map[string]Kind{"app.py": Removed}, kinds(changes))
}

// A directory reopened, as a resumed session does, finds the checkpoints it took.
func TestPrivate_KeepsCheckpointsAcrossARestart(t *testing.T) {
	ctx := t.Context()
	dir, store := plain(t), t.TempDir()
	write(t, dir, "a.txt", "one\n")
	first, err := Private(ctx, dir, store)
	require.NoError(t, err)
	before, err := first.Capture(ctx)
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second, err := Private(ctx, dir, store)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })
	write(t, dir, "a.txt", "two\n")
	require.NoError(t, second.RestoreTo(ctx, before, ""))
	assert.Equal(t, "one\n", read(t, dir, "a.txt"))
}

// The home directory or a tree too large to hash each request is refused up
// front, rather than stalling every request on a capture.
func TestPrivate_RefusesWhatIsTooBroad(t *testing.T) {
	home := plain(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // where Windows looks for home
	_, err := Private(t.Context(), home, t.TempDir())
	require.ErrorIs(t, err, ErrTooBroad)

	was := maxPrivateFiles
	maxPrivateFiles = 3
	t.Cleanup(func() { maxPrivateFiles = was })
	dir := plain(t)
	for _, n := range []string{"a", "b", "c", "d"} {
		write(t, dir, n, n)
	}
	write(t, dir, "node_modules/x", "not counted\n")
	_, err = Private(t.Context(), dir, t.TempDir())
	require.ErrorIs(t, err, ErrTooBroad)
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

// plain is a directory outside any git repository.
func plain(t *testing.T) string {
	t.Helper()
	dir := realpath(t, t.TempDir())
	require.False(t, Available(t.Context(), dir), "the temp directory sits in a repository")
	return dir
}

func private(t *testing.T, dir string) *Dir {
	t.Helper()
	d, err := Private(t.Context(), dir, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// nested is a repository with one commit at dir/name.
func nested(t *testing.T, dir, name string) {
	t.Helper()
	at := filepath.Join(dir, name)
	write(t, at, "f", "committed\n")
	git(t, at, "init", "-q")
	git(t, at, "config", "user.email", "t@example.com")
	git(t, at, "config", "user.name", "t")
	commit(t, at)
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
	cmd.Env = env(nil)
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
