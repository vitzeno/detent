package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

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

// r asks for a reviewer on exactly what the modal shows: its scope, its trees,
// the request it was for, and the files as the endpoint should read them.
func TestReview_RStartsAReviewerOnWhatTheModalShows(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.m.run.Subagents = true
	k.openReview("/review")
	k.intentOf(t, event.LoadDiffKind)
	files := []event.FileDiff{{Path: "a.go", Change: event.FileModified, Hunks: []event.Hunk{{Header: "@@ -1 +1 @@",
		Lines: []event.DiffLine{{Op: event.LineAdded, New: 1, Text: "\tx := 1\r"}}}}}}
	k.m.apply(event.DiffLoaded{Base: "b2", Head: "e2", Files: files})
	k.press(t, "r")
	got := k.intentOf(t, event.ReviewChangesKind).(event.ReviewChanges)
	assert.Equal(t, event.ReviewChanges{Review: k.m.review.id, Reviewed: k.m.blocks[1].id, Scope: event.ScopeRequest,
		Base: "b2", Head: "e2", Request: 2, Asked: "edit", Files: files}, got,
		"the file's own bytes, tab and CR, not the screen's")
}

func TestReview_RIsRefusedWithoutAReviewerOrMidRequest(t *testing.T) {
	k := loadedReview(t)
	k.press(t, "r")
	k.noIntent(t)
	assert.Contains(t, k.m.notice.text, "subagents on")

	k.m.run.Subagents = true
	k.m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 3, Prompt: "running"})
	k.press(t, "r")
	k.noIntent(t)
	assert.Contains(t, k.m.notice.text, "a request is running")
}

// A review is one line in history, saying what it reviewed and how many
// comments it left, and enter on it opens it on its own trees.
func TestReview_IsOneLineInHistoryThatOpensIt(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	review, turn := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, Prompt: "review of request 2", Review: review})
	for range 2 {
		k.m.apply(event.ReviewCommented{Review: review, Reviewed: k.m.blocks[1].id, Base: "b2", Head: "e2",
			Scope: event.ScopeRequest, Op: event.CommentAdded, Comment: event.ReviewComment{ID: uuid.Must(uuid.NewV7()), Author: "reviewer"}})
	}
	k.m.sizeViewport()
	lines, _ := k.m.historyAll()
	assert.Contains(t, ansi.Strip(strings.Join(lines, "\n")), "review of request 2",
		"a running review still names what it reviews in a narrow pane")
	k.m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone})
	k.m.sizeViewport()
	lines, _ = k.m.historyAll()
	history := ansi.Strip(strings.Join(lines, "\n"))
	assert.Equal(t, 1, strings.Count(history, "review of request 2"), "one line, no prompt above it")
	assert.Contains(t, history, "review of request 2 · 2 comments")

	k.m.nav.cursor = len(k.m.rows()) - 1
	k.m.nav.focus = focusHistory
	k.press(t, "enter")
	assert.Equal(t, modeReview, k.m.mode)
	assert.Equal(t, review, k.m.review.id)
	assert.Equal(t, event.LoadDiff{Base: "b2", Head: "e2"}, k.intentOf(t, event.LoadDiffKind))
}

// left and right step between reviews, oldest first, each on its own trees.
func TestReview_ArrowsStepBetweenReviews(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	ids := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	for i, id := range ids {
		k.m.apply(event.ReviewCommented{Review: id, Reviewed: k.m.blocks[i].id, Base: []string{"b1", "b2"}[i],
			Head: []string{"e1", "e2"}[i], Scope: event.ScopeRequest, Op: event.CommentAdded,
			Comment: event.ReviewComment{ID: uuid.Must(uuid.NewV7())}})
	}
	var cmd tea.Cmd
	k.m, cmd = k.m.openReview(k.m.reviews[1])
	runCmd(cmd)
	k.intentOf(t, event.LoadDiffKind)
	k.send(t, tea.KeyPressMsg{Code: tea.KeyLeft})
	assert.Equal(t, ids[0], k.m.review.id)
	assert.Equal(t, event.LoadDiff{Base: "b1", Head: "e1"}, k.intentOf(t, event.LoadDiffKind))
	k.send(t, tea.KeyPressMsg{Code: tea.KeyLeft})
	k.noIntent(t)
	assert.Equal(t, ids[0], k.m.review.id, "nothing older")
	k.send(t, tea.KeyPressMsg{Code: tea.KeyRight})
	assert.Equal(t, ids[1], k.m.review.id)
}

// A sent review is read, not written: comments, edits and a reviewer are refused.
func TestReview_ASentReviewIsReadOnly(t *testing.T) {
	k := loadedReview(t)
	k.m.apply(event.ReviewCommented{Review: k.m.review.id, Op: event.CommentAdded,
		Comment: event.ReviewComment{ID: uuid.Must(uuid.NewV7()), Path: "ui/facts.go", Side: "new", Start: 10, End: 10}})
	k.m.apply(event.ReviewSubmitted{Review: k.m.review.id, Comments: 1})
	k.openReview("/review")
	k.intentOf(t, event.LoadDiffKind)
	var cmd tea.Cmd
	k.m, cmd = k.m.openReview(k.m.reviews[0])
	runCmd(cmd)
	k.intentOf(t, event.LoadDiffKind)
	k.m.apply(loadedDiff("b2", "e2"))
	k.press(t, "tab")
	k.press(t, "down")
	for _, key := range []string{"c", "r"} {
		k.press(t, key)
		assert.Nil(t, k.m.review.edit)
		assert.Contains(t, k.m.notice.text, "was sent")
	}
	k.noIntent(t)
}

// Undo passes over a review's line to the request before it, since a review
// changed nothing to take back.
func TestUndo_PassesOverAReview(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	turn := uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, Prompt: "review of request 2", Review: uuid.Must(uuid.NewV7())})
	k.m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone})
	target, _ := k.m.undoTarget("")
	require.NotNil(t, target)
	assert.Equal(t, 2, target.n)
}

// A branch review opened by name keeps its id when main has moved under it.
func TestReview_ABranchReviewOpenedByNameKeepsItsID(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	id := uuid.Must(uuid.NewV7())
	k.m.apply(event.ReviewCommented{Review: id, Base: "old-base", Scope: event.ScopeBranch, Against: "main",
		Op: event.CommentAdded, Comment: event.ReviewComment{ID: uuid.Must(uuid.NewV7())}})
	var cmd tea.Cmd
	k.m, cmd = k.m.openReview(k.m.reviews[0])
	runCmd(cmd)
	assert.Equal(t, event.LoadDiff{Branch: true, Against: "main"}, k.intentOf(t, event.LoadDiffKind))
	k.m.apply(event.DiffLoaded{Branch: true, Base: "new-base", Against: "main"})
	assert.Equal(t, id, k.m.review.id)
}

// The reviewer is a subagent: in the agents block while it works, a opening it in
// the inspector, and once it ends the review's line, enter on which opens the review.
func TestReview_TheReviewerIsASubagentWhileItWorks(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	review, turn, agent := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	k.m.apply(event.ReviewCommented{Review: review, Reviewed: k.m.blocks[1].id, Base: "b2", Head: "e2",
		Scope: event.ScopeRequest, Op: event.CommentAdded, Comment: event.ReviewComment{ID: uuid.Must(uuid.NewV7())}})
	k.m.apply(event.TurnStarted{Turn: turn, Prompt: "review of request 2", Review: review})
	k.m.apply(event.AgentStarted{Agent: agent, Turn: turn, Name: "reviewer", Task: "review of request 2"})
	k.m.sizeViewport()

	pinned := k.m.pinned()
	require.Len(t, pinned, 1)
	assert.Equal(t, "reviewer", pinned[0].name)
	lines, _ := k.m.historyAll()
	assert.NotContains(t, ansi.Strip(strings.Join(lines, "\n")), "review of request 2", "drawn in the agents block instead")
	k.m.nav.focus = focusHistory
	k.m.prompt.Blur()
	k.press(t, "a")
	require.Equal(t, modeInspector, k.m.mode)
	assert.Equal(t, agent, k.m.insp.agent.id)
	k.press(t, "esc")

	k.m.apply(event.AgentEnded{Agent: agent, Reason: event.AgentDone})
	k.m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone})
	k.m.sizeViewport()
	assert.Empty(t, k.m.pinned())
	lines, _ = k.m.historyAll()
	assert.Contains(t, ansi.Strip(strings.Join(lines, "\n")), "review of request 2 · 1 comment")
	k.m.nav.cursor, k.m.nav.focus = len(k.m.rows())-1, focusHistory
	k.press(t, "enter")
	assert.Equal(t, modeReview, k.m.mode, "the review, not the inspector")
}

// A reviewer looks like what it is: its own colour, a glyph that breathes rather
// than spins, and the files it has read out of the diff where a context gauge would be.
func TestReview_TheReviewerLooksLikeAReviewer(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	review, turn, agent, step := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, Prompt: "review of request 2", Review: review})
	k.m.apply(event.ReviewStarted{Review: review, Scope: event.ScopeRequest, Files: 3})
	k.m.apply(event.AgentStarted{Agent: agent, Turn: turn, Name: "reviewer"})
	k.m.apply(event.StepStarted{Turn: turn, Step: step, N: 1, Agent: agent})
	for _, p := range []string{"a.go", "b.go", "a.go"} {
		k.m.apply(event.ToolCallProposed{Step: step, ToolCall: uuid.Must(uuid.NewV7()), Tool: event.ToolReviewDiff,
			Args: map[string]any{"path": p}, Agent: agent})
	}
	k.m.apply(event.ReviewCommented{Review: review, Op: event.CommentAdded, Comment: event.ReviewComment{ID: uuid.Must(uuid.NewV7())}})

	a := k.m.agents[agent]
	row := k.m.pinnedRow(a, 60)
	assert.Contains(t, row, toolName.review.Render(fmt.Sprintf("%-*s", pinnedName, "reviewer")))
	assert.Contains(t, ansi.Strip(row), "2/3 ✎1", "a.go read twice is one file")
	assert.NotContains(t, ansi.Strip(row), "%")
	first := ansi.Strip(k.m.agentGlyph(a))
	k.m.pulse += 4
	assert.NotEqual(t, first, ansi.Strip(k.m.agentGlyph(a)), "it breathes")
	assert.NotEqual(t, ansi.Strip(k.m.spinner.View()), first)
}

// A comment that just came in stands out and settles. One replayed from the
// store came in long ago, so it never does.
func TestReview_ANewCommentStandsOutThenSettles(t *testing.T) {
	k := loadedReview(t)
	id := k.m.review.id
	c := event.ReviewComment{ID: uuid.Must(uuid.NewV7()), Author: "reviewer", Path: "ui/facts.go", Side: "new", Start: 10, End: 10, Body: "x"}
	k.m.apply(event.ReviewCommented{Review: id, Op: event.CommentAdded, Comment: c})
	row := diffRow{comment: &k.m.reviewByID(id).comments[0], author: true}
	fresh := k.m.commentLine(row)
	require.NotNil(t, k.m.fadeTick(), "a tick to fade it by")
	assert.Nil(t, k.m.fadeTick(), "and only one")

	k.m.reviewByID(id).arrived[c.ID] = time.Now().Add(-fadeFor / 2)
	midway := k.m.commentLine(row)
	k.m.reviewByID(id).arrived[c.ID] = time.Now().Add(-2 * fadeFor)
	settled := k.m.commentLine(row)
	assert.NotEqual(t, fresh, midway, "lit, then only coloured")
	assert.NotEqual(t, midway, settled, "then settled")
	k.m.fadeTicking = false
	assert.Nil(t, k.m.fadeTick(), "nothing left to fade")

	replayed := New(t.Context(), event.New(), SessionInfo{}).Restore(asRecords([]event.Event{
		event.ReviewCommented{Review: id, Op: event.CommentAdded, Comment: c}}))
	assert.Empty(t, replayed.reviews[0].arrived)
}

// The file list shows where the reviewer is: the file it reads pulses, the ones
// it has read are dotted.
func TestReview_TheFileListShowsWhereTheReviewerIs(t *testing.T) {
	k := loadedReview(t)
	turn, agent, step := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, Prompt: "review of request 2", Review: k.m.review.id})
	k.m.apply(event.ReviewStarted{Review: k.m.review.id, Scope: event.ScopeRequest, Files: 2})
	k.m.apply(event.AgentStarted{Agent: agent, Turn: turn, Name: "reviewer"})
	k.m.apply(event.StepStarted{Turn: turn, Step: step, N: 1, Agent: agent})
	for _, p := range []string{"README.md", "ui/facts.go"} {
		k.m.apply(event.ToolCallProposed{Step: step, ToolCall: uuid.Must(uuid.NewV7()), Tool: event.ToolReviewDiff,
			Args: map[string]any{"path": p}, Agent: agent})
	}
	lines := k.m.reviewFileLines(40, 10)
	assert.Contains(t, ansi.Strip(lines[0]), "◉ M", "reading ui/facts.go")
	assert.Contains(t, ansi.Strip(lines[1]), "· A", "README.md read")
	k.m.pulse += 4
	assert.Contains(t, ansi.Strip(k.m.reviewFileLines(40, 10)[0]), "○ M", "and it pulses")
}

// Once the reviewer finishes, its verdict sits above the diff, a few lines of it.
func TestReview_TheVerdictSitsAboveTheDiff(t *testing.T) {
	k := loadedReview(t)
	k.m.apply(event.ReviewCommented{Review: k.m.review.id, Op: event.CommentAdded, Comment: event.ReviewComment{
		ID: uuid.Must(uuid.NewV7()), Author: "reviewer", Body: strings.Repeat("a long verdict word ", 40)}})
	lines := k.m.reviewDiffLines(30)
	assert.Contains(t, ansi.Strip(lines[0]), "reviewer's verdict")
	assert.True(t, strings.HasSuffix(ansi.Strip(lines[maxVerdictLines]), "…"), "cut to a few lines")
	assert.Contains(t, ansi.Strip(lines[maxVerdictLines+2]), "@@ -10,2 +10,2 @@", "then the diff")
}

// t walks the reviewer's comments on lines in file and line order, skipping
// the human's and the verdict: y keeps, n drops, e rewrites, and the end says how it went.
func TestReview_TriageWalksTheReviewersComments(t *testing.T) {
	k := loadedReview(t)
	id := k.m.review.id
	add := func(c event.ReviewComment) uuid.UUID {
		c.ID = uuid.Must(uuid.NewV7())
		k.m.apply(event.ReviewCommented{Review: id, Op: event.CommentAdded, Comment: c})
		return c.ID
	}
	readme := add(event.ReviewComment{Author: "reviewer", Path: "README.md", Side: "new", Start: 1, End: 1, Body: "typo"})
	late := add(event.ReviewComment{Author: "reviewer", Path: "ui/facts.go", Side: "new", Start: 40, End: 40, Body: "dead code"})
	early := add(event.ReviewComment{Author: "reviewer", Path: "ui/facts.go", Side: "new", Start: 10, End: 10, Body: "unclear"})
	add(event.ReviewComment{Path: "ui/facts.go", Side: "new", Start: 10, End: 10, Body: "mine"})
	add(event.ReviewComment{Author: "reviewer", Body: "the verdict"})

	k.press(t, "t")
	require.NotNil(t, k.m.review.triage)
	assert.Equal(t, []uuid.UUID{early, late, readme}, k.m.review.triage.queue, "files in order, then lines")
	assert.Equal(t, early, k.m.reviewRows()[k.m.review.line].comment.ID, "the cursor on the first")

	k.press(t, "y")
	assert.Equal(t, late, k.m.reviewRows()[k.m.review.line].comment.ID)
	k.press(t, "n")
	del := k.intentOf(t, event.CommentReviewKind).(event.CommentReview)
	assert.Equal(t, event.CommentDeleted, del.Op)
	assert.Equal(t, late, del.Comment.ID)
	assert.Equal(t, 1, k.m.review.file, "on to README.md")

	k.press(t, "e")
	require.NotNil(t, k.m.review.edit)
	k.typeText(t, " here")
	k.press(t, "enter")
	edit := k.intentOf(t, event.CommentReviewKind).(event.CommentReview)
	assert.Equal(t, event.CommentEdited, edit.Op)
	assert.Equal(t, readme, edit.Comment.ID)
	assert.Nil(t, k.m.review.triage, "that was the last")
	assert.Contains(t, k.m.notice.text, "kept 2, dropped 1")
	assert.Equal(t, modeReview, k.m.mode)
}

func TestReview_TriageNeedsReviewerCommentsAndEscOnlyStopsIt(t *testing.T) {
	k := loadedReview(t)
	k.press(t, "t")
	assert.Nil(t, k.m.review.triage)
	assert.Contains(t, k.m.notice.text, "no reviewer comments")

	k.m.apply(event.ReviewCommented{Review: k.m.review.id, Op: event.CommentAdded, Comment: event.ReviewComment{
		ID: uuid.Must(uuid.NewV7()), Author: "reviewer", Path: "README.md", Side: "new", Start: 1, End: 1}})
	k.press(t, "t")
	require.NotNil(t, k.m.review.triage)
	k.press(t, "esc")
	assert.Nil(t, k.m.review.triage)
	assert.Equal(t, modeReview, k.m.mode, "the walk stopped, not the review")
	k.noIntent(t)
}

// A reviewer that has not commented yet can still be found again: /review alone
// opens it, and the scope it reviews leads back to it rather than to a new review.
func TestReview_ARunningReviewCanBeFoundAgain(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	review, turn := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, Prompt: "review of this branch against main", Review: review})
	k.m.apply(event.ReviewStarted{Review: review, Scope: event.ScopeBranch, Base: "mb", Against: "main", Files: 9})

	k.openReview("/review")
	assert.Equal(t, review, k.m.review.id, "the running review, not the last request")
	assert.Equal(t, event.LoadDiff{Branch: true, Against: "main"}, k.intentOf(t, event.LoadDiffKind))
	k.press(t, "esc")

	k.m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone})
	k.openReview("/review branch main")
	k.intentOf(t, event.LoadDiffKind)
	k.m.apply(event.DiffLoaded{Branch: true, Base: "mb", Against: "main"})
	assert.Equal(t, review, k.m.review.id, "its scope leads back to it")
}

// A review stored before comments named their scope opens as what it was, and
// one whose request is gone still draws: the arrows panicked on both.
func TestReview_AnOldOrOrphanedReviewOpensWithoutAPanic(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	branch, orphan := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	k.m.apply(event.ReviewCommented{Review: branch, Base: "mb", Op: event.CommentAdded,
		Comment: event.ReviewComment{ID: uuid.Must(uuid.NewV7())}})
	k.m.apply(event.ReviewCommented{Review: orphan, Reviewed: uuid.Must(uuid.NewV7()), Base: "b9", Head: "e9",
		Op: event.CommentAdded, Comment: event.ReviewComment{ID: uuid.Must(uuid.NewV7())}})

	var cmd tea.Cmd
	k.m, cmd = k.m.openReview(k.m.reviews[0])
	runCmd(cmd)
	assert.Equal(t, event.LoadDiff{Branch: true}, k.intentOf(t, event.LoadDiffKind),
		"read as the branch it was, never a checkpoint against a commit")
	assert.NotPanics(t, func() { k.m.withOverlay(k.m.baseView()) })

	k.send(t, tea.KeyPressMsg{Code: tea.KeyRight})
	k.intentOf(t, event.LoadDiffKind)
	assert.Equal(t, orphan, k.m.review.id)
	assert.NotPanics(t, func() { k.m.withOverlay(k.m.baseView()) })
	assert.Contains(t, ansi.Strip(k.m.reviewTitle()), "a request no longer here")
}

// A reviewer's context is measured against the whole window it is given, not a
// child's share, or the inspector would call it full while it had room to spare.
func TestReview_TheReviewersContextIsOfTheWholeWindow(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	k.m.run.ContextTokens, k.m.run.ChildContextTokens = 200_000, 50_000
	review, turn, agent := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, Prompt: "review of request 2", Review: review})
	k.m.apply(event.AgentStarted{Agent: agent, Turn: turn, Name: "reviewer"})
	k.m.agents[agent].ctx = 100_000
	assert.Equal(t, 50, k.m.agentContext(k.m.agents[agent]))
}

// > and < step through the comments on lines across files, in file and line
// order, from wherever the cursor is, and say when there are no more.
func TestReview_ArrowsJumpBetweenComments(t *testing.T) {
	k := loadedReview(t)
	id := k.m.review.id
	add := func(path string, line int) uuid.UUID {
		c := event.ReviewComment{ID: uuid.Must(uuid.NewV7()), Path: path, Side: "new", Start: line, End: line, Body: "x"}
		k.m.apply(event.ReviewCommented{Review: id, Op: event.CommentAdded, Comment: c})
		return c.ID
	}
	readme := add("README.md", 1)
	far := add("ui/facts.go", 40)
	near := add("ui/facts.go", 10)
	at := func() uuid.UUID {
		row := k.m.reviewRows()[k.m.review.line]
		require.NotNil(t, row.comment, "the cursor is on a comment")
		return row.comment.ID
	}

	for _, want := range []uuid.UUID{near, far, readme} {
		k.press(t, ">")
		assert.Equal(t, want, at())
	}
	k.press(t, ">")
	assert.Equal(t, readme, at(), "nothing after the last")
	assert.Contains(t, k.m.notice.text, "no more comments")
	k.press(t, "<")
	assert.Equal(t, far, at(), "and back across files")
	assert.Equal(t, 0, k.m.review.file)

	k.press(t, "p")
	for i, row := range k.m.reviewRows() {
		if row.line >= 0 && row.comment == nil && k.m.review.files[0].Hunks[row.hunk].Lines[row.line].New == 40 {
			k.m.review.line = i
		}
	}
	require.Nil(t, k.m.reviewRows()[k.m.review.line].comment, "on line 40 itself")
	k.press(t, "<")
	assert.Equal(t, near, at(), "from a line, the nearest comment before it")
}

// A single esc never stops a request: the one closing a modal does nothing to
// it, the next only asks, and the one after that, soon enough, stops it.
func TestEsc_OneNeverStopsARequestAndTwoDo(t *testing.T) {
	k := loadedReview(t)
	turn := uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, Prompt: "review of request 2", Review: k.m.review.id})
	k.press(t, "esc")
	require.Equal(t, modeInput, k.m.mode)
	k.press(t, "esc")
	k.noIntent(t)
	assert.Contains(t, k.m.statusHint(), "[esc] again stops the request")

	k.press(t, "esc")
	assert.Equal(t, event.Abort{Turn: turn}, k.intentOf(t, event.AbortKind))
}

// The second esc must come soon and straight after: any other key, or waiting,
// drops the ask, and the bar says which esc will do.
func TestEsc_TheAskLapses(t *testing.T) {
	k := newKeyed(t)
	turn := uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, N: 1, Prompt: "build"})
	assert.Contains(t, k.m.statusHint(), "[esc][esc] stops the request")
	k.press(t, "esc")
	k.press(t, "x")
	k.press(t, "esc")
	k.noIntent(t)

	k.m.escArmed = time.Now().Add(-escTwice)
	k.press(t, "esc")
	k.noIntent(t)
	assert.Contains(t, k.m.statusHint(), "[esc] again stops")
}

// Wherever esc does something, the bar starts by saying what, as onEscape does it.
func TestEsc_TheBarLeadsWithWhatEscWillDo(t *testing.T) {
	k := newKeyed(t)
	k.m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "build"})
	k.m.apply(event.TurnEnded{Turn: k.m.blocks[0].id, Reason: event.EndDone})
	assert.NotContains(t, k.m.statusHint(), "[esc]", "idle, esc does nothing")

	for _, c := range []struct {
		name string
		set  func(m *Model)
		want string
	}{
		{"a page open", func(m *Model) { m.panel.open = panelStatus }, "[esc] closes the page"},
		{"the output pane, idle", func(m *Model) { m.nav.focus = focusOutput }, "[esc] back to history"},
		{"the finder", func(m *Model) { m.mode = modeFinder }, "[esc] closes"},
		{"the resume picker", func(m *Model) { m.mode = modeResume }, "[esc] closes"},
		{"a request running", func(m *Model) { m.cur = m.blocks[0] }, "[esc][esc] stops the request"},
	} {
		m := k.m
		c.set(&m)
		assert.True(t, strings.HasPrefix(m.statusHint(), c.want), "%s: %q", c.name, m.statusHint())
	}
}

// At the step bound, esc used to answer "stop here", a single esc ending the request.
func TestBound_EscDoesNotStopTheRequest(t *testing.T) {
	k := newKeyed(t)
	turn := uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, N: 1, Prompt: "build"})
	k.m.apply(event.BoundReached{Turn: turn, Steps: 100})
	k.m.askedAt = time.Now().Add(-time.Hour)
	k.press(t, "esc")
	k.noIntent(t)
	assert.Equal(t, modeBound, k.m.mode)
}

// A review fills the output pane, not "(no output)": how its reviewer stands,
// its verdict and its comments by where they are, while it works and after.
func TestReview_FillsTheOutputPane(t *testing.T) {
	k := reviewable(t, "e1", "e2")
	review, turn, agent, step := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, Prompt: "review of request 2", Review: review})
	k.m.apply(event.ReviewStarted{Review: review, Scope: event.ScopeRequest, Files: 3})
	k.m.apply(event.AgentStarted{Agent: agent, Turn: turn, Name: "reviewer"})
	k.m.apply(event.StepStarted{Turn: turn, Step: step, N: 1, Agent: agent})
	k.m.apply(event.ToolCallProposed{Step: step, ToolCall: uuid.Must(uuid.NewV7()), Tool: event.ToolReviewDiff,
		Args: map[string]any{"path": "a.go"}, Agent: agent})
	k.m.apply(event.ReviewCommented{Review: review, Op: event.CommentAdded, Comment: event.ReviewComment{
		ID: uuid.Must(uuid.NewV7()), Author: "reviewer", Path: "a.go", Side: "new", Start: 7, End: 7, Body: "drops the resumed case"}})
	k.m.sizeViewport()
	running := ansi.Strip(k.m.output.View())
	assert.Contains(t, running, "review of request 2")
	assert.Contains(t, running, "1/3 files · now a.go")
	assert.Contains(t, running, "a.go:7 reviewer drops the resumed case")

	k.m.apply(event.ReviewCommented{Review: review, Op: event.CommentAdded, Comment: event.ReviewComment{
		ID: uuid.Must(uuid.NewV7()), Author: "reviewer", Body: "one real problem"}})
	k.m.apply(event.AgentEnded{Agent: agent, Reason: event.AgentDone})
	k.m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone})
	k.m.nav.cursor, k.m.nav.focus = len(k.m.rows())-1, focusHistory
	k.m.sizeViewport()
	done := ansi.Strip(k.m.output.View())
	assert.Contains(t, done, "verdict")
	assert.Contains(t, done, "one real problem")
	assert.Contains(t, done, "1 comment on lines")
	assert.NotContains(t, done, "(no output)")
}

// Closing the modal leaves the reviewer working: its comments still land on
// the review, and its row counts them.
func TestReview_TheModalClosesWhileTheReviewerWorks(t *testing.T) {
	k := loadedReview(t)
	id := k.m.review.id
	turn := uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, Prompt: "review of request 2", Review: id})
	k.press(t, "esc")
	k.press(t, "esc")
	require.Equal(t, modeInput, k.m.mode)
	k.m.apply(event.ReviewCommented{Review: id, Op: event.CommentAdded, Comment: event.ReviewComment{ID: uuid.Must(uuid.NewV7())}})
	assert.Len(t, k.m.reviewByID(id).comments, 1)
	assert.Equal(t, 1, k.m.blockByID(turn).rows[0].comments)
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
