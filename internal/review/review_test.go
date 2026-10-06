package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/worktree"
)

// Through real git: each file is named and numbered as the patch says, odd
// names included, since the parser reads paths out of git's headers.
func TestWatch_LoadsTheChangesBetweenTwoCheckpoints(t *testing.T) {
	names := []string{"with space.txt", "ünï.txt"}
	if runtime.GOOS != "windows" {
		names = append(names, "tab\there.txt", `quote".txt`)
	}
	dir := repo(t)
	d, err := worktree.Open(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	before, err := d.Capture(t.Context())
	require.NoError(t, err)
	write(t, dir, "tracked.txt", "one\nTWO\nthree\n")
	for _, n := range names {
		write(t, dir, n, "new\n")
	}

	got := ask(t, d, event.LoadDiff{Base: string(before)})
	require.Empty(t, got.Err)
	byPath := map[string]event.FileDiff{}
	for _, f := range got.Files {
		byPath[f.Path] = f
	}
	for _, n := range names {
		require.Contains(t, byPath, n)
		assert.Equal(t, event.FileAdded, byPath[n].Change, n)
	}
	tracked := byPath["tracked.txt"]
	assert.Equal(t, event.FileModified, tracked.Change)
	require.Len(t, tracked.Hunks, 1)
	assert.Equal(t, []event.DiffLine{
		{Op: event.LineContext, Old: 1, New: 1, Text: "one"},
		{Op: event.LineRemoved, Old: 2, Text: "two"},
		{Op: event.LineAdded, New: 2, Text: "TWO"},
		{Op: event.LineContext, Old: 3, New: 3, Text: "three"},
	}, tracked.Hunks[0].Lines)
}

// A branch runs from where it left main to the files now, new ones included, and
// a file a clean filter stores (as git-lfs does) is unchanged until edited.
func TestWatch_LoadsABranchAgainstMain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the filter is a tr command")
	}
	dir := repo(t)
	gitIn(t, dir, "config", "filter.up.clean", "tr a-z A-Z")
	gitIn(t, dir, "config", "filter.up.smudge", "tr A-Z a-z")
	write(t, dir, ".gitattributes", "*.dat filter=up\n")
	write(t, dir, "x.dat", "hello\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "filtered")
	gitIn(t, dir, "branch", "-M", "main")
	start := strings.TrimSpace(gitIn(t, dir, "rev-parse", "HEAD"))
	gitIn(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "committed.txt", "c\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "c")
	write(t, dir, "tracked.txt", "one\nTWO\nthree\n")
	write(t, dir, "untracked.txt", "u\n")
	d, err := worktree.Open(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	got := ask(t, d, event.LoadDiff{Branch: true})
	require.Empty(t, got.Err)
	assert.Equal(t, start, got.Base)
	assert.Equal(t, "main", got.Against)
	var paths []string
	for _, f := range got.Files {
		paths = append(paths, f.Path)
	}
	assert.Equal(t, []string{"committed.txt", "tracked.txt", "untracked.txt"}, paths)
}

// A pruned checkpoint is said as what it means to the human, not git's words.
func TestWatch_SaysWhenTheFilesArePruned(t *testing.T) {
	d, err := worktree.Open(t.Context(), repo(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	got := ask(t, d, event.LoadDiff{Base: "0123456789abcdef0123456789abcdef01234567"})
	assert.Contains(t, got.Err, "pruned")
	assert.Empty(t, got.Files)
}

// A comment is recorded as a fact, which is what the store keeps and the modal draws.
func TestWatch_RecordsACommentAsAFact(t *testing.T) {
	bus, seen := watching(t)
	c := event.CommentReview{Review: uuid.Must(uuid.NewV7()), Reviewed: uuid.Must(uuid.NewV7()),
		Base: "b", Head: "h", Op: event.CommentAdded,
		Comment: event.ReviewComment{ID: uuid.Must(uuid.NewV7()), Path: "a.go", Side: "new", Start: 2, End: 2, Body: "x"}}
	bus.Publish(c)
	assert.Equal(t, event.ReviewCommented{Review: c.Review, Reviewed: c.Reviewed, Base: "b", Head: "h",
		Op: event.CommentAdded, Comment: c.Comment}, next(t, seen))
}

// Submitting closes the review before it becomes the next prompt.
func TestWatch_SubmittingClosesTheReviewThenSendsIt(t *testing.T) {
	bus, seen := watching(t)
	id := uuid.Must(uuid.NewV7())
	bus.Publish(event.SubmitReview{Review: id, Request: 2, Comments: []event.ReviewComment{
		{ID: uuid.Must(uuid.NewV7()), Path: "a.go", Side: "new", Start: 1, End: 1, Quote: "+x", Body: "no"}}})
	assert.Equal(t, event.ReviewSubmitted{Review: id, Comments: 1}, next(t, seen))
	p, ok := next(t, seen).(event.SubmitPrompt)
	require.True(t, ok)
	assert.Contains(t, p.Text, "a.go:1")
}

// The human's comments are instructions and a reviewer's are opinions, each
// under what it quotes, and a reply sits under the comment it answers.
func TestPrompt_KeepsTheHumansCommentsApartFromAReviewers(t *testing.T) {
	mine, theirs, edited := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	got := prompt(event.SubmitReview{Request: 3, Comments: []event.ReviewComment{
		{ID: mine, Path: "ui/facts.go", Side: "old", Start: 118, End: 118, Quote: "-m.resumed(v)",
			Body: "why was this deleted?"},
		{ID: theirs, Author: "reviewer", Path: "tool/registry.go", Side: "new", Start: 88, End: 90,
			Quote: "+a\n+b\n+c", Body: "Only copies\nevery spec"},
		{ID: uuid.Must(uuid.NewV7()), ReplyTo: theirs, Body: "share them, as Without does"},
		{ID: edited, Path: "a.go", Side: "new", Start: 4, End: 4, Quote: "+x", Body: "rename it",
			Original: "this name is unclear"},
	}})
	assert.Equal(t, `Review of your changes in request 3.

From the human. Act on these:

ui/facts.go:118 (removed lines, numbered as before the change)
    -m.resumed(v)
  why was this deleted?

a.go:4
    +x
  rename it
  (the human rewrote a reviewer's comment, which said: this name is unclear)

From a reviewer agent, kept by the human. Weigh each, and say where you disagree:

tool/registry.go:88-90
    +a
    +b
    +c
  Only copies
  every spec
  > the human: share them, as Without does`, got)
}

// The opening says whose changes these are, so the agent never takes the
// human's work for its own.
func TestPrompt_OpensWithWhoseChangesTheseAre(t *testing.T) {
	for scope, want := range map[event.ReviewScope]string{
		event.ScopeRequest: "Review of your changes in request 3.",
		event.ScopeSession: "Review of everything you changed this session, up to request 3.",
		event.ScopeSince:   "Review of changes the human made since your request 3 ended. You did not write these.",
		event.ScopeBranch:  "Review of this branch against main, committed and not, which holds the human's work as well as yours.",
	} {
		got := prompt(event.SubmitReview{Scope: scope, Request: 3, Against: "main"})
		assert.Equal(t, want, got, scope)
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		name  string
		patch string
		cut   bool
		want  []event.FileDiff
	}{
		{
			name: "a deleted file, numbered on the old side only",
			patch: "diff --git a/gone.txt b/gone.txt\ndeleted file mode 100644\nindex 1..0\n" +
				"--- a/gone.txt\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-a\n-b\n",
			want: []event.FileDiff{{Path: "gone.txt", Change: event.FileDeleted, Hunks: []event.Hunk{{
				Header: "@@ -1,2 +0,0 @@",
				Lines:  []event.DiffLine{{Op: event.LineRemoved, Old: 1, Text: "a"}, {Op: event.LineRemoved, Old: 2, Text: "b"}},
			}}}},
		},
		{
			name:  "a binary file, listed with no hunks",
			patch: "diff --git a/x.png b/x.png\nnew file mode 100644\nindex 0..1\nBinary files /dev/null and b/x.png differ\n",
			want:  []event.FileDiff{{Path: "x.png", Change: event.FileAdded, Binary: true}},
		},
		{
			name:  "no newline at the end belongs to the line before",
			patch: "diff --git a/a b/a\nindex 1..2 100644\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-x\n\\ No newline at end of file\n+y\n",
			want: []event.FileDiff{{Path: "a", Change: event.FileModified, Hunks: []event.Hunk{{
				Header: "@@ -1 +1 @@",
				Lines:  []event.DiffLine{{Op: event.LineRemoved, Old: 1, Text: "x"}, {Op: event.LineAdded, New: 1, Text: "y"}},
			}}}},
		},
		{
			name:  "content that looks like a header stays content",
			patch: "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1,2 @@\n x\n+diff --git a/b b/b\n",
			want: []event.FileDiff{{Path: "a", Change: event.FileModified, Hunks: []event.Hunk{{
				Header: "@@ -1 +1,2 @@",
				Lines: []event.DiffLine{{Op: event.LineContext, Old: 1, New: 1, Text: "x"},
					{Op: event.LineAdded, New: 2, Text: "diff --git a/b b/b"}},
			}}}},
		},
		{
			name:  "the last file of a cut patch is listed without its hunks",
			patch: "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-x\n+y\ndiff --git a/b b/b\n--- a/b\n+++ b/b\n@@ -1,9 +1,9 @@\n-p\n",
			cut:   true,
			want: []event.FileDiff{
				{Path: "a", Change: event.FileModified, Hunks: []event.Hunk{{Header: "@@ -1 +1 @@",
					Lines: []event.DiffLine{{Op: event.LineRemoved, Old: 1, Text: "x"}, {Op: event.LineAdded, New: 1, Text: "y"}}}}},
				{Path: "b", Change: event.FileModified, Cut: true},
			},
		},
		{
			name:  "a quoted path",
			patch: "diff --git \"a/tab\\there.txt\" \"b/tab\\there.txt\"\nnew file mode 100644\n",
			want:  []event.FileDiff{{Path: "tab\there.txt", Change: event.FileAdded}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, parse(c.patch, c.cut))
		})
	}
}

// A file too long to review line by line is listed, not drawn.
func TestParse_ListsAnOverlongFileWithoutItsHunks(t *testing.T) {
	patch := "diff --git a/lock b/lock\n--- a/lock\n+++ b/lock\n@@ -0,0 +1,9999 @@\n" +
		strings.Repeat("+x\n", maxFileLines+1)
	got := parse(patch, false)
	require.Len(t, got, 1)
	assert.True(t, got[0].Cut)
	assert.Empty(t, got[0].Hunks)
}

// A patch is git's output, but its lines are file contents nothing controls.
func FuzzParse(f *testing.F) {
	f.Add("diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1,2 +1,2 @@\n x\n-y\n+z\n", false)
	f.Add("diff --git \"a/\\303\\274\" \"b/\\303\\274\"\nBinary files a and b differ\n", true)
	f.Add("diff --git \"a/x\n@@ -9 +9 @@\n\\\n", false)
	f.Fuzz(func(t *testing.T, patch string, cut bool) {
		for _, file := range parse(patch, cut) {
			for _, h := range file.Hunks {
				for _, l := range h.Lines {
					switch l.Op {
					case event.LineContext, event.LineAdded, event.LineRemoved:
					default:
						t.Fatalf("a line marked %q", l.Op)
					}
				}
			}
		}
	})
}

func ask(t *testing.T, files Patcher, v event.LoadDiff) event.DiffLoaded {
	t.Helper()
	bus := event.New()
	answers, unsub := bus.Subscribe(event.Only(event.DiffLoadedKind))
	stop := Watch(context.Background(), bus, files)
	t.Cleanup(func() { stop(); unsub(); bus.Close() })
	bus.Publish(v)
	select {
	case rec := <-answers:
		return rec.Event.(event.DiffLoaded)
	case <-time.After(5 * time.Second):
		t.Fatal("LoadDiff was never answered")
		return event.DiffLoaded{}
	}
}

// watching is a bus with Watch on it and a feed of the facts it publishes.
func watching(t *testing.T) (*event.Bus, <-chan event.Record) {
	t.Helper()
	bus := event.New()
	seen, unsub := bus.Subscribe(event.Only(event.ReviewCommentedKind, event.ReviewSubmittedKind,
		event.SubmitPromptKind))
	stop := Watch(context.Background(), bus, nil)
	t.Cleanup(func() { stop(); unsub(); bus.Close() })
	return bus, seen
}

func next(t *testing.T, seen <-chan event.Record) event.Event {
	t.Helper()
	select {
	case rec := <-seen:
		return rec.Event
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was published")
		return nil
	}
}

func repo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	write(t, dir, "tracked.txt", "one\ntwo\nthree\n")
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "config", "user.email", "t@example.com")
	gitIn(t, dir, "config", "user.name", "t")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "x")
	return dir
}

// gitIn runs git as the human would in dir, attributes and filters on.
func gitIn(t *testing.T, dir string, args ...string) string {
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

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644))
}
