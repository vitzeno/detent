package tool

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/vitzeno/detent/event"
)

// The review tools are how a reviewer agent reads the diff the human sees and
// comments on it. The engine answers both from that diff, so they run nothing.

const (
	ReviewDiffName    = "review_diff"
	ReviewCommentName = "review_comment"
)

// ReviewReads is every other tool a reviewer may call: reads of the files, and
// nothing that writes or reaches the network, where an injected line could send them.
var ReviewReads = []string{"read_file", "grep", "find_files", "list_dir"}

// ReviewDiff shows one changed file's hunks, each line numbered.
type ReviewDiff struct{ files []event.FileDiff }

// NewReviewDiff answers from files, the diff under review.
func NewReviewDiff(files []event.FileDiff) ReviewDiff { return ReviewDiff{files: files} }

func (ReviewDiff) Name() string { return ReviewDiffName }

func (ReviewDiff) Describe() Spec {
	return Spec{
		Description: "Show the changes under review to one file. Each line is numbered on the side it is on: " +
			"the old number for a removed line, the new for an added one, and both for an unchanged one.",
		Params:     []Param{{Name: "path", Type: TypeString, Required: true, Desc: "a file from the list of changed files"}},
		Mutability: event.MutRead,
		Internal:   true,
		Group:      "review",
	}
}

func (ReviewDiff) Lower(a Args) (string, error) { return event.Command(ReviewDiffName, a), nil }

// Answer is path's hunks, or which files there are when it is not one of them.
func (r ReviewDiff) Answer(a Args) (string, error) {
	f, err := find(r.files, a.String("path"))
	if err != nil {
		return "", err
	}
	switch {
	case f.Binary:
		return "A binary file: its changes cannot be shown.", nil
	case f.Cut:
		return "Too long to review line by line: read the file itself if it matters.", nil
	case len(f.Hunks) == 0:
		return "Only its mode changed.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s)\n", f.Path, f.Change)
	for _, h := range f.Hunks {
		b.WriteString(h.Header + "\n")
		for _, l := range h.Lines {
			fmt.Fprintf(&b, "%5s %5s %c%s\n", lineNo(l.Old), lineNo(l.New), l.Op, l.Text)
		}
	}
	return b.String(), nil
}

// ReviewComment checks a comment against the diff, so every comment has the
// lines it is about to sit under.
type ReviewComment struct{ files []event.FileDiff }

// NewReviewComment checks against files, the diff under review.
func NewReviewComment(files []event.FileDiff) ReviewComment { return ReviewComment{files: files} }

func (ReviewComment) Name() string { return ReviewCommentName }

func (ReviewComment) Describe() Spec {
	return Spec{
		Description: "Comment on lines of the diff under review. Number them as review_diff does: side old for " +
			"removed lines and their old numbers, side new for anything else and its new numbers. The lines " +
			"start to end must all be in one hunk. Say what is wrong and why, in a sentence or two.",
		Params: []Param{
			{Name: "path", Type: TypeString, Required: true, Desc: "the file"},
			{Name: "side", Type: TypeString, Required: true, Enum: []string{"old", "new"},
				Desc: "old for removed lines, new for added or unchanged ones"},
			{Name: "start", Type: TypeInt, Required: true, Desc: "the first line's number on that side"},
			{Name: "end", Type: TypeInt, Required: true, Desc: "the last line's number, the same as start for one line"},
			{Name: "body", Type: TypeString, Required: true, Desc: "the comment"},
		},
		Mutability: event.MutRead,
		Internal:   true,
		Group:      "review",
	}
}

func (ReviewComment) Lower(a Args) (string, error) { return event.Command(ReviewCommentName, a), nil }

// Check is the comment a call makes, quoting its lines, or why there are no
// such lines to comment on.
func (r ReviewComment) Check(a Args) (event.ReviewComment, error) {
	f, err := find(r.files, a.String("path"))
	if err != nil {
		return event.ReviewComment{}, err
	}
	side, start, end := a.String("side"), a.Int("start", 0), a.Int("end", 0)
	body := strings.TrimSpace(a.String("body"))
	switch {
	case f.Binary || f.Cut:
		return event.ReviewComment{}, fmt.Errorf("%s is not shown line by line, so its lines cannot be commented on", f.Path)
	case body == "":
		return event.ReviewComment{}, fmt.Errorf("the comment's body is empty")
	case start < 1 || end < start:
		return event.ReviewComment{}, fmt.Errorf("lines %d to %d are not a range: start at 1 or more, and end at start or after", start, end)
	}
	for _, h := range f.Hunks {
		first, last, found := -1, -1, 0
		for i, l := range h.Lines {
			n := l.New
			if side == "old" {
				n = l.Old
			}
			if n < start || n > end {
				continue
			}
			if first < 0 {
				first = i
			}
			last, found = i, found+1
		}
		if found == end-start+1 {
			// Every line between, so a removed line inside a new-side range is quoted too.
			quote := make([]string, 0, last-first+1)
			for _, l := range h.Lines[first : last+1] {
				quote = append(quote, string(rune(l.Op))+l.Text)
			}
			return event.ReviewComment{Path: f.Path, Side: side, Start: start, End: end,
				Quote: strings.Join(quote, "\n"), Body: body}, nil
		}
	}
	return event.ReviewComment{}, fmt.Errorf("lines %d to %d on the %s side of %s are not all in one hunk: "+
		"read its numbers with review_diff and comment on lines it shows", start, end, side, f.Path)
}

// find is the changed file at path, or which files there are.
func find(files []event.FileDiff, path string) (*event.FileDiff, error) {
	i := slices.IndexFunc(files, func(f event.FileDiff) bool { return f.Path == path })
	if i < 0 {
		names := make([]string, 0, len(files))
		for _, f := range files {
			names = append(names, f.Path)
		}
		return nil, fmt.Errorf("%q is not among the changed files: %s", path, strings.Join(names, ", "))
	}
	return &files[i], nil
}

func lineNo(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}
