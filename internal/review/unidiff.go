package review

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/vitzeno/detent/event"
)

// maxFileLines is the most diff lines one file is drawn with. A lockfile or a
// generated file past it is listed, since nobody reviews it line by line.
const maxFileLines = 5000

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// parse reads a patch from git diff-tree -p with renames off. cut marks the
// last file, which the patch's limit may have ended part way through.
func parse(patch string, cut bool) []event.FileDiff {
	var files []event.FileDiff
	var f *event.FileDiff
	var h *event.Hunk
	oldNo, newNo := 0, 0
	for line := range strings.SplitSeq(patch, "\n") {
		if rest, ok := strings.CutPrefix(line, "diff --git "); ok {
			files = append(files, event.FileDiff{Path: headerPath(rest), Change: event.FileModified})
			f, h = &files[len(files)-1], nil
			continue
		}
		if f == nil {
			continue
		}
		if h == nil {
			switch {
			case strings.HasPrefix(line, "new file mode"):
				f.Change = event.FileAdded
			case strings.HasPrefix(line, "deleted file mode"):
				f.Change = event.FileDeleted
			case strings.HasPrefix(line, "Binary files "):
				f.Binary = true
			}
		}
		if m := hunkHeader.FindStringSubmatch(line); m != nil {
			oldNo, _ = strconv.Atoi(m[1])
			newNo, _ = strconv.Atoi(m[2])
			f.Hunks = append(f.Hunks, event.Hunk{Header: line})
			h = &f.Hunks[len(f.Hunks)-1]
			continue
		}
		if h == nil || line == "" {
			continue
		}
		l := event.DiffLine{Op: event.LineOp(line[0]), Text: line[1:]}
		switch l.Op {
		case event.LineContext:
			l.Old, l.New = oldNo, newNo
			oldNo++
			newNo++
		case event.LineRemoved:
			l.Old = oldNo
			oldNo++
		case event.LineAdded:
			l.New = newNo
			newNo++
		default:
			// "\ No newline at end of file", which belongs to the line before it.
			continue
		}
		h.Lines = append(h.Lines, l)
	}
	for i := range files {
		if lines(files[i]) > maxFileLines || cut && i == len(files)-1 {
			files[i].Cut, files[i].Hunks = true, nil
		}
	}
	return files
}

// headerPath reads the path from "a/P b/P", or from its quoted form when git
// quoted a name holding a tab, a quote or a newline.
func headerPath(rest string) string {
	if strings.HasPrefix(rest, `"`) {
		end := closingQuote(rest)
		if p, err := strconv.Unquote(rest[:end+1]); err == nil {
			return strings.TrimPrefix(p, "a/")
		}
		return rest
	}
	// Renames are off, so both halves name the same path: "a/" P " b/" P.
	n := (len(rest) - len("a/ b/")) / 2
	if n <= 0 || len(rest) < 2+n {
		return rest
	}
	return rest[2 : 2+n]
}

// closingQuote is the index of the quote ending the one rest opens with.
func closingQuote(rest string) int {
	for i := 1; i < len(rest); i++ {
		switch rest[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return len(rest) - 1
}

func lines(f event.FileDiff) int {
	n := 0
	for _, h := range f.Hunks {
		n += len(h.Lines)
	}
	return n
}
