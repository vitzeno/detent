package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// /review reads the last request between the files as it began and as it ended.
func TestReview_OpensOnTheLastRequestBetweenItsTwoCheckpoints(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.openReview("/review")
	assert.Equal(t, modeReview, k.m.mode)
	assert.Equal(t, event.LoadDiff{Base: "b2", Head: "e2"}, k.intentOf(t, event.LoadDiffKind))
}

func TestReview_NamesARequestByItsNumber(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.openReview("/review 1")
	assert.Equal(t, event.LoadDiff{Base: "b1", Head: "e1"}, k.intentOf(t, event.LoadDiffKind))
}

func TestReview_SaysWhenThereIsNoSuchRequest(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.openReview("/review 9")
	k.noIntent(t)
	assert.Equal(t, modeInput, k.m.mode)
	assert.Contains(t, k.m.notice.text, "no request 9")
}

// A request stored before TurnEnded named its files ends where the next began,
// and the last such request ends at the files now.
func TestReview_AnOldSessionReadsToTheNextRequestsStart(t *testing.T) {
	k := reviewable(t, "", "")
	k.openReview("/review 1")
	assert.Equal(t, event.LoadDiff{Base: "b1", Head: "b2"}, k.intentOf(t, event.LoadDiffKind))
	k.press(t, "esc")
	k.openReview("/review")
	assert.Equal(t, event.LoadDiff{Base: "b2"}, k.intentOf(t, event.LoadDiffKind))
	assert.Contains(t, ansi.Strip(k.m.reviewTitle()), "to your files now")
}

// The files on the left with what changed in each, the selected one's lines
// on the right, numbered on both sides.
func TestReview_DrawsTheFilesAndTheSelectedDiff(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.openReview("/review")
	k.m.apply(loadedDiff("b2", "e2"))
	screen := ansi.Strip(k.m.withOverlay(k.m.baseView()))
	assert.Contains(t, screen, "▸ M ui/facts.go", "a path that fits is never cut")
	assert.Contains(t, screen, "+1 −1")
	assert.Contains(t, screen, "-old line")
	assert.Contains(t, screen, "+new line")
	assert.Contains(t, screen, "README.md")
}

// An answer for another pair of trees, a late one, never fills this review.
func TestReview_IgnoresAnAnswerForOtherTrees(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.openReview("/review")
	k.m.apply(loadedDiff("b1", "e1"))
	assert.True(t, k.m.review.loading)
	assert.Empty(t, k.m.review.files)
}

// File contents are untrusted: an escape is shown, never sent, and a CRLF
// line does not end in a visible \r.
func TestReview_DefusesWhatTheFilesSay(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.openReview("/review")
	k.m.apply(event.DiffLoaded{Base: "b2", Head: "e2", Files: []event.FileDiff{{
		Path: "evil\x1b[2J.txt", Change: event.FileAdded, Hunks: []event.Hunk{{Header: "@@ -0,0 +1 @@",
			Lines: []event.DiffLine{{Op: event.LineAdded, New: 1, Text: "a\x1b]52;c;aGk=\x07b\r"}}}},
	}}})
	f := k.m.review.files[0]
	assert.NotContains(t, f.Path, "\x1b")
	assert.Equal(t, "a^[]52;c;aGk=^Gb", f.Hunks[0].Lines[0].Text, "escapes shown, and no ^M from the CRLF")
}

// n and p pick files, tab moves the arrows to the diff, and ] and [ jump hunks.
func TestReview_KeysMoveThroughFilesAndHunks(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.openReview("/review")
	k.intentOf(t, event.LoadDiffKind)
	k.m.apply(loadedDiff("b2", "e2"))
	k.press(t, "n")
	assert.Equal(t, 1, k.m.review.file)
	k.press(t, "p")
	k.press(t, "tab")
	require.True(t, k.m.review.diffFocused)
	k.press(t, "]")
	assert.Equal(t, 3, k.m.review.line, "the second hunk's header, after the first's two lines")
	k.press(t, "[")
	assert.Equal(t, 0, k.m.review.line)
	k.press(t, "down")
	assert.Equal(t, 1, k.m.review.line)
	k.press(t, "esc")
	assert.Equal(t, modeInput, k.m.mode)
	k.noIntent(t)
}

// A replayed request keeps its trees, so a resumed session can be reviewed,
// but is never undoable, since its snapshot died with the old process.
func TestRestore_KeepsTheTreesToReviewButNotUndo(t *testing.T) {
	turn := uuid.Must(uuid.NewV7())
	m := New(t.Context(), event.New(), SessionInfo{}).Restore(asRecords([]event.Event{
		event.SessionStarted{Session: uuid.Must(uuid.NewV7()), Model: "m"},
		event.TurnStarted{Turn: turn, N: 1, Prompt: "edit"},
		event.CheckpointTaken{Turn: turn, Snapshot: "s", Tree: "b1"},
		event.TurnEnded{Turn: turn, Reason: event.EndDone, Tree: "e1"},
	}))
	b := m.block(turn)
	require.NotNil(t, b)
	assert.Equal(t, "b1", b.base)
	assert.Equal(t, "e1", b.tree)
	assert.False(t, b.undoable)
	assert.False(t, b.files || b.container)
}

// c on a line opens the editor, enter sends the comment over that line, and
// once recorded it is drawn under the line it is about.
func TestReview_CommentsOnALineAndShowsItUnderIt(t *testing.T) {
	k := loadedReview(t)
	k.press(t, "down")
	k.press(t, "c")
	k.typeText(t, "why")
	k.press(t, "enter")
	got := k.intentOf(t, event.CommentReviewKind).(event.CommentReview)
	assert.Equal(t, k.m.review.id, got.Review)
	assert.Equal(t, k.m.blocks[1].id, got.Reviewed)
	assert.Equal(t, event.CommentAdded, got.Op)
	want := got.Comment
	want.ID = uuid.Nil
	assert.Equal(t, event.ReviewComment{Path: "ui/facts.go", Side: "old", Start: 10, End: 10,
		Quote: "-old line", Body: "why"}, want, "every line removed, so numbered as before")

	k.m.apply(commented(got))
	rows := k.m.reviewRows()
	require.NotNil(t, rows[2].comment, "under the line it is about")
	screen := ansi.Strip(k.m.withOverlay(k.m.baseView()))
	assert.Regexp(t, `-old line\s*│[^\n]*\n[^┃]*┃ you\s[^\n]*\n[^┃]*┃ why\s`, screen, "its author, then its words")
	assert.Contains(t, screen, "✎1", "and counted beside its file")
}

// v starts a range, and a comment over it covers every line between, numbered
// on the new side once any line is not a removed one.
func TestReview_ARangeCoversTheLinesBetween(t *testing.T) {
	k := loadedReview(t)
	k.press(t, "down")
	k.press(t, "v")
	k.press(t, "down")
	k.press(t, "c")
	k.typeText(t, "both")
	k.press(t, "enter")
	c := k.intentOf(t, event.CommentReviewKind).(event.CommentReview).Comment
	assert.Equal(t, "new", c.Side)
	assert.Equal(t, [2]int{10, 10}, [2]int{c.Start, c.End})
	assert.Equal(t, "-old line\n+new line", c.Quote)
}

func TestReview_ARangeStaysInOneHunk(t *testing.T) {
	k := loadedReview(t)
	k.press(t, "down")
	k.press(t, "v")
	k.press(t, "]")
	k.press(t, "down")
	k.press(t, "c")
	assert.Nil(t, k.m.review.edit)
	assert.Contains(t, k.m.notice.text, "one hunk")
}

// An empty comment, or one dropped with esc, sends nothing.
func TestReview_AnEmptyOrDroppedCommentSendsNothing(t *testing.T) {
	k := loadedReview(t)
	k.press(t, "down")
	k.press(t, "c")
	k.press(t, "enter")
	k.press(t, "c")
	k.typeText(t, "no")
	k.press(t, "esc")
	k.noIntent(t)
	assert.Equal(t, modeReview, k.m.mode, "esc dropped the draft, not the review")
}

// On a reviewer's comment: c replies to it, e rewrites it, which makes it the
// human's with the reviewer's words kept, and x deletes it on the second press.
func TestReview_RepliesEditsAndDeletesAComment(t *testing.T) {
	k := loadedReview(t)
	theirs := event.ReviewComment{ID: uuid.Must(uuid.NewV7()), Author: "reviewer", Path: "ui/facts.go",
		Side: "new", Start: 10, End: 10, Quote: "+new line", Body: "unclear"}
	k.m.apply(event.ReviewCommented{Review: k.m.review.id, Reviewed: k.m.blocks[1].id, Base: "b2", Head: "e2",
		Op: event.CommentAdded, Comment: theirs})
	k.press(t, "down")
	k.press(t, "down")
	k.press(t, "down")
	require.NotNil(t, k.m.reviewRows()[k.m.review.line].comment, "on the comment")

	k.press(t, "c")
	k.typeText(t, "agreed")
	k.press(t, "enter")
	reply := k.intentOf(t, event.CommentReviewKind).(event.CommentReview).Comment
	assert.Equal(t, theirs.ID, reply.ReplyTo)
	assert.Equal(t, "agreed", reply.Body)

	k.press(t, "e")
	for range len("unclear") {
		k.m, _ = k.m.update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	k.typeText(t, "rename it")
	k.press(t, "enter")
	edit := k.intentOf(t, event.CommentReviewKind).(event.CommentReview)
	assert.Equal(t, event.CommentEdited, edit.Op)
	assert.Empty(t, edit.Comment.Author, "rewritten, it is the human's")
	assert.Equal(t, "unclear", edit.Comment.Original)
	assert.Equal(t, "rename it", edit.Comment.Body)

	k.press(t, "x")
	k.noIntent(t)
	k.press(t, "x")
	del := k.intentOf(t, event.CommentReviewKind).(event.CommentReview)
	assert.Equal(t, event.CommentDeleted, del.Op)
	assert.Equal(t, theirs.ID, del.Comment.ID)
}

// A long comment wraps inside the pane, past the gutter, and never loses words.
func TestReview_ALongCommentWrapsWhole(t *testing.T) {
	k := loadedReview(t)
	body := "this renames the field but the store still reads the old key, so a resumed session loses it"
	k.m.apply(event.ReviewCommented{Review: k.m.review.id, Op: event.CommentAdded, Comment: event.ReviewComment{
		ID: uuid.Must(uuid.NewV7()), Author: "reviewer", Path: "ui/facts.go", Side: "new", Start: 10, End: 10,
		Body: body}})
	var words []string
	for _, l := range k.m.reviewDiffLines(30) {
		l = ansi.Strip(l)
		if _, text, ok := strings.Cut(l, "┃ "); ok && !strings.HasPrefix(text, "reviewer") {
			assert.NotContains(t, text, "…", "a comment is wrapped, never cut")
			assert.LessOrEqual(t, ansi.StringWidth(l), k.m.reviewTextWidth())
			words = append(words, strings.Fields(text)...)
		}
	}
	assert.Equal(t, strings.Fields(body), words)
}

// A deleted comment takes its replies with it.
func TestReview_DeletingACommentDropsItsReplies(t *testing.T) {
	k := loadedReview(t)
	parent := event.ReviewComment{ID: uuid.Must(uuid.NewV7()), Path: "ui/facts.go", Side: "new", Start: 10, End: 10}
	for _, c := range []event.ReviewComment{parent, {ID: uuid.Must(uuid.NewV7()), ReplyTo: parent.ID}} {
		k.m.apply(event.ReviewCommented{Review: k.m.review.id, Op: event.CommentAdded, Comment: c})
	}
	k.m.apply(event.ReviewCommented{Review: k.m.review.id, Op: event.CommentDeleted, Comment: event.ReviewComment{ID: parent.ID}})
	assert.Empty(t, k.m.reviewByID(k.m.review.id).comments)
}

// ctrl+s sends every comment, and once the review is recorded as sent the
// modal closes and says so.
func TestReview_SubmitSendsTheCommentsAndCloses(t *testing.T) {
	k := loadedReview(t)
	k.ctrl(t, 's')
	k.noIntent(t)
	assert.Contains(t, k.m.notice.text, "nothing to send")

	c := event.ReviewComment{ID: uuid.Must(uuid.NewV7()), Path: "ui/facts.go", Side: "new", Start: 10, End: 10, Body: "x"}
	k.m.apply(event.ReviewCommented{Review: k.m.review.id, Op: event.CommentAdded, Comment: c})
	k.ctrl(t, 's')
	sub := k.intentOf(t, event.SubmitReviewKind).(event.SubmitReview)
	assert.Equal(t, 2, sub.Request)
	assert.Equal(t, []event.ReviewComment{c}, sub.Comments)

	k.m.apply(event.ReviewSubmitted{Review: sub.Review, Comments: 1})
	assert.Equal(t, modeInput, k.m.mode)
	assert.Contains(t, k.m.notice.text, "sent 1 comment")
}

// /review again finds the open review, and once that is sent starts another.
func TestReview_ReopensTheOpenReviewAndStartsAnotherOnceSent(t *testing.T) {
	k := loadedReview(t)
	first := k.m.review.id
	k.m.apply(event.ReviewCommented{Review: first, Reviewed: k.m.blocks[1].id, Base: "b2", Head: "e2",
		Op: event.CommentAdded, Comment: event.ReviewComment{ID: uuid.Must(uuid.NewV7())}})
	k.press(t, "esc")
	k.press(t, "esc")
	k.openReview("/review")
	assert.Equal(t, first, k.m.review.id)

	k.m.apply(event.ReviewSubmitted{Review: first, Comments: 1})
	k.openReview("/review")
	assert.NotEqual(t, first, k.m.review.id)
}

// Undoing a request drops its reviews, and a replay brings them back otherwise.
func TestReview_UndoDropsTheUndoneRequestsReviews(t *testing.T) {
	k := loadedReview(t)
	turn := k.m.blocks[1].id
	added := event.ReviewCommented{Review: k.m.review.id, Reviewed: turn, Base: "b2", Head: "e2",
		Op: event.CommentAdded, Comment: event.ReviewComment{ID: uuid.Must(uuid.NewV7()), Body: "kept"}}
	k.m.apply(added)

	replayed := New(t.Context(), event.New(), SessionInfo{}).Restore(asRecords([]event.Event{
		event.TurnStarted{Turn: turn, N: 1, Prompt: "edit"}, added}))
	require.Len(t, replayed.reviews, 1)
	assert.Equal(t, "kept", replayed.reviews[0].comments[0].Body)

	k.m.apply(event.RolledBack{Turn: turn})
	assert.Empty(t, k.m.reviews)
}

// A reviewer's words are a model's, shown rather than sent to the terminal.
func TestReview_DefusesAComment(t *testing.T) {
	k := loadedReview(t)
	k.m.apply(event.ReviewCommented{Review: k.m.review.id, Op: event.CommentAdded,
		Comment: event.ReviewComment{ID: uuid.Must(uuid.NewV7()), Author: "r\x1b[2J", Body: "b\x1b]52;c;x\x07"}})
	c := k.m.reviewByID(k.m.review.id).comments[0]
	assert.NotContains(t, c.Author+c.Body, "\x1b")
}

// /review alone passes over a last request that changed nothing, such as one
// that only answered a review, to the newest that did.
func TestReview_StepsBackPastARequestThatChangedNothing(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.openReview("/review")
	k.intentOf(t, event.LoadDiffKind)
	k.update(t, factMsg{events: []event.Event{event.DiffLoaded{Base: "b2", Head: "e2"}}})
	assert.Equal(t, event.LoadDiff{Base: "b1", Head: "e1"}, k.intentOf(t, event.LoadDiffKind))
	k.update(t, factMsg{events: []event.Event{event.DiffLoaded{Base: "b1", Head: "e1"}}})
	k.noIntent(t)
	assert.Equal(t, 1, k.m.review.block.n)

	k.press(t, "esc")
	k.openReview("/review 2")
	k.intentOf(t, event.LoadDiffKind)
	k.update(t, factMsg{events: []event.Event{event.DiffLoaded{Base: "b2", Head: "e2"}}})
	k.noIntent(t)
}

// s steps through request, session, since and branch, each asking for its own
// pair of trees, the session ending where since begins.
func TestReview_SCyclesThroughTheScopes(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.openReview("/review")
	k.intentOf(t, event.LoadDiffKind)
	for _, want := range []event.LoadDiff{
		{Base: "b1", Head: "e2"},
		{Base: "e2"},
		{Branch: true},
		{Base: "b2", Head: "e2"},
	} {
		k.press(t, "s")
		assert.Equal(t, want, k.intentOf(t, event.LoadDiffKind))
	}
}

// A scope with nothing of its own is skipped: one request has no session
// beyond itself, and nothing is "since" while a request runs.
func TestReview_SkipsAScopeWithNothingToShow(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.m.blocks = k.m.blocks[1:]
	k.openReview("/review")
	k.intentOf(t, event.LoadDiffKind)
	k.press(t, "s")
	assert.Equal(t, event.ScopeSince, k.m.review.scope, "no session of one request")
	k.intentOf(t, event.LoadDiffKind)

	k.press(t, "s")
	k.intentOf(t, event.LoadDiffKind)
	k.press(t, "s")
	require.Equal(t, event.ScopeRequest, k.m.review.scope)
	k.intentOf(t, event.LoadDiffKind)

	k.m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 3, Prompt: "running"})
	k.press(t, "s")
	assert.Equal(t, event.ScopeBranch, k.m.review.scope, "nothing since, while a request runs")
}

// Each scope has its own review, found again on the way back round.
func TestReview_EachScopeKeepsItsOwnReview(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.openReview("/review")
	request := k.m.review.id
	k.m.apply(event.ReviewCommented{Review: request, Reviewed: k.m.blocks[1].id, Base: "b2", Head: "e2",
		Scope: event.ScopeRequest, Op: event.CommentAdded, Comment: event.ReviewComment{ID: uuid.Must(uuid.NewV7())}})
	k.press(t, "s")
	assert.NotEqual(t, request, k.m.review.id)
	for range 3 {
		k.press(t, "s")
	}
	assert.Equal(t, request, k.m.review.id)
}

// A branch is compared with main or a named ref. Its review is found by where
// it left that ref, belongs to no request, and is sent as the branch's.
func TestReview_ABranchIsReviewedAgainstWhereItLeftARef(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.openReview("/review branch main")
	assert.Equal(t, event.LoadDiff{Branch: true, Against: "main"}, k.intentOf(t, event.LoadDiffKind))
	k.m.apply(event.DiffLoaded{Branch: true, Base: "mb", Against: "main", Files: loadedDiff("", "").Files})
	assert.Equal(t, "mb", k.m.review.base)
	assert.Contains(t, ansi.Strip(k.m.reviewTitle()), "this branch against main")

	k.press(t, "tab")
	k.press(t, "down")
	k.press(t, "c")
	k.typeText(t, "hm")
	k.press(t, "enter")
	c := k.intentOf(t, event.CommentReviewKind).(event.CommentReview)
	assert.Equal(t, uuid.Nil, c.Reviewed)
	assert.Equal(t, event.ScopeBranch, c.Scope)
	assert.Equal(t, "mb", c.Base)

	k.m.apply(commented(c))
	k.ctrl(t, 's')
	sub := k.intentOf(t, event.SubmitReviewKind).(event.SubmitReview)
	assert.Equal(t, event.ScopeBranch, sub.Scope)
	assert.Equal(t, "main", sub.Against)

	k.press(t, "esc")
	k.press(t, "esc")
	k.openReview("/review branch main")
	k.intentOf(t, event.LoadDiffKind)
	k.m.apply(event.DiffLoaded{Branch: true, Base: "mb", Against: "main"})
	assert.Equal(t, c.Review, k.m.review.id, "the same branch point finds the same review")
}

// No request owns a branch's review, so undoing one leaves it alone.
func TestReview_UndoLeavesABranchReview(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.m.apply(event.ReviewCommented{Review: uuid.Must(uuid.NewV7()), Base: "mb", Scope: event.ScopeBranch,
		Op: event.CommentAdded, Comment: event.ReviewComment{ID: uuid.Must(uuid.NewV7())}})
	k.m.apply(event.RolledBack{Turn: k.m.blocks[1].id})
	assert.Len(t, k.m.reviews, 1)
}

// /review session and /review since open straight onto those scopes.
func TestReview_OpensOntoAScopeByName(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.openReview("/review session")
	assert.Equal(t, event.LoadDiff{Base: "b1", Head: "e2"}, k.intentOf(t, event.LoadDiffKind))
	k.press(t, "esc")
	k.openReview("/review since")
	assert.Equal(t, event.LoadDiff{Base: "e2"}, k.intentOf(t, event.LoadDiffKind))
	assert.Contains(t, ansi.Strip(k.m.reviewTitle()), "your edits since request 2")
}

// openReview runs a /review line and what it asks for.
func (k *keyed) openReview(line string) {
	var cmd tea.Cmd
	k.m, cmd = k.m.showReview(line)
	runCmd(cmd)
}

// reviewable is a session with two requests that changed files, ending at the
// trees named, "" for one stored before TurnEnded said.
func reviewable(t *testing.T, end1, end2 string) *keyed {
	t.Helper()
	k := newKeyed(t)
	k.m.apply(event.SessionStarted{Session: uuid.Must(uuid.NewV7()), Model: "m"})
	for i, end := range []string{end1, end2} {
		turn := uuid.Must(uuid.NewV7())
		k.m.apply(event.TurnStarted{Turn: turn, N: i + 1, Prompt: "edit"})
		k.m.apply(event.CheckpointTaken{Turn: turn, Tree: []string{"b1", "b2"}[i]})
		k.m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone, Tree: end})
	}
	k.m.sizeViewport()
	return k
}

// loadedDiff answers a review with two files, the first with two hunks.
func loadedDiff(base, head string) event.DiffLoaded {
	return event.DiffLoaded{Base: base, Head: head, Files: []event.FileDiff{
		{Path: "ui/facts.go", Change: event.FileModified, Hunks: []event.Hunk{
			{Header: "@@ -10,2 +10,2 @@", Lines: []event.DiffLine{
				{Op: event.LineRemoved, Old: 10, Text: "old line"},
				{Op: event.LineAdded, New: 10, Text: "new line"},
			}},
			{Header: "@@ -40 +40 @@", Lines: []event.DiffLine{{Op: event.LineContext, Old: 40, New: 40, Text: "same"}}},
		}},
		{Path: "README.md", Change: event.FileAdded, Hunks: []event.Hunk{
			{Header: "@@ -0,0 +1 @@", Lines: []event.DiffLine{{Op: event.LineAdded, New: 1, Text: "# hi"}}},
		}},
	}}
}

// loadedReview is the review of request 2 with its diff loaded and the diff pane focused.
func loadedReview(t *testing.T) *keyed {
	t.Helper()
	k := reviewable(t, "e1", "e2")
	k.openReview("/review")
	k.intentOf(t, event.LoadDiffKind)
	k.m.apply(loadedDiff("b2", "e2"))
	k.press(t, "tab")
	return k
}

// commented is the fact internal/review records for a comment.
func commented(v event.CommentReview) event.ReviewCommented {
	return event.ReviewCommented{Review: v.Review, Reviewed: v.Reviewed, Base: v.Base, Head: v.Head,
		Op: v.Op, Comment: v.Comment}
}

// typeText types into the editor. Its commands only blink the cursor, so
// they are not run, which would wait on each.
func (k *keyed) typeText(t *testing.T, s string) {
	t.Helper()
	for _, r := range s {
		k.m, _ = k.m.update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (k *keyed) update(t *testing.T, msg tea.Msg) {
	t.Helper()
	var cmd tea.Cmd
	k.m, cmd = k.m.update(msg)
	runCmd(cmd)
}
