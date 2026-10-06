package ui

import (
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
