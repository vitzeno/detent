package tool

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// review_diff numbers each line on the side it is on, as review_comment counts them.
func TestReviewDiff_NumbersEachLineOnItsSide(t *testing.T) {
	got, err := NewReviewDiff(reviewFiles()).Answer(Args{"path": "a.go"})
	require.NoError(t, err)
	assert.Equal(t, "a.go (modified)\n@@ -10,3 +10,3 @@\n"+
		"   10    10  keep\n"+
		"   11       -old\n"+
		"         11 +new\n"+
		"   12    12  tail\n"+
		"@@ -40 +40 @@\n"+
		"   40    40  far\n", got)
}

func TestReviewDiff_NamesTheFilesWhenThePathIsNotOne(t *testing.T) {
	_, err := NewReviewDiff(reviewFiles()).Answer(Args{"path": "nope.go"})
	assert.ErrorContains(t, err, "a.go, img.png")
}

// A comment is only made on lines of one hunk, numbered as review_diff shows them,
// and quotes every line between its first and last.
func TestReviewComment_ChecksItsLinesAgainstTheDiff(t *testing.T) {
	c := NewReviewComment(reviewFiles())
	got, err := c.Check(Args{"path": "a.go", "side": "new", "start": 10, "end": 11, "body": " why? "})
	require.NoError(t, err)
	assert.Equal(t, event.ReviewComment{Path: "a.go", Side: "new", Start: 10, End: 11,
		Quote: " keep\n-old\n+new", Body: "why?"}, got)

	got, err = c.Check(Args{"path": "a.go", "side": "old", "start": 11, "end": 11, "body": "gone"})
	require.NoError(t, err)
	assert.Equal(t, "-old", got.Quote)

	for name, args := range map[string]Args{
		"across hunks":                {"path": "a.go", "side": "new", "start": 12, "end": 40, "body": "x"},
		"not in the diff":             {"path": "a.go", "side": "new", "start": 20, "end": 20, "body": "x"},
		"an added line, numbered old": {"path": "a.go", "side": "old", "start": 13, "end": 13, "body": "x"},
		"backwards":                   {"path": "a.go", "side": "new", "start": 11, "end": 10, "body": "x"},
		"no body":                     {"path": "a.go", "side": "new", "start": 10, "end": 10, "body": "  "},
		"a binary file":               {"path": "img.png", "side": "new", "start": 1, "end": 1, "body": "x"},
		"another file":                {"path": "b.go", "side": "new", "start": 1, "end": 1, "body": "x"},
	} {
		_, err := c.Check(args)
		assert.Error(t, err, name)
	}
}

// Both run nothing, so the registry marks them for the engine to answer.
func TestReviewTools_AreAnsweredByTheEngine(t *testing.T) {
	r := Standard()
	require.NoError(t, r.Register(NewReviewDiff(nil)))
	require.NoError(t, r.Register(NewReviewComment(nil)))
	call, err := r.Prepare(ReviewCommentName, map[string]any{"path": "a.go", "side": "new", "start": 1, "end": 1, "body": "x"})
	require.NoError(t, err)
	assert.True(t, call.Internal)
	assert.Equal(t, event.MutRead, call.Mutability)
	_, err = r.Prepare(ReviewCommentName, map[string]any{"path": "a.go", "side": "left", "start": 1, "end": 1, "body": "x"})
	assert.Error(t, err, "side is old or new")
}

func reviewFiles() []event.FileDiff {
	return []event.FileDiff{
		{Path: "a.go", Change: event.FileModified, Hunks: []event.Hunk{
			{Header: "@@ -10,3 +10,3 @@", Lines: []event.DiffLine{
				{Op: event.LineContext, Old: 10, New: 10, Text: "keep"},
				{Op: event.LineRemoved, Old: 11, Text: "old"},
				{Op: event.LineAdded, New: 11, Text: "new"},
				{Op: event.LineContext, Old: 12, New: 12, Text: "tail"},
			}},
			{Header: "@@ -40 +40 @@", Lines: []event.DiffLine{{Op: event.LineContext, Old: 40, New: 40, Text: "far"}}},
		}},
		{Path: "img.png", Change: event.FileAdded, Binary: true},
	}
}
