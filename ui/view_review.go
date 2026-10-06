package ui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/island"
	"github.com/vitzeno/detent/ui/layout"
)

// The review's box: the changed files on the left, the selected one's diff on
// the right with its comments under the lines they are about.

// commentEditorLines is how tall the comment being written is drawn.
const commentEditorLines = 3

func (m Model) reviewBox() string {
	left, right := m.reviewPaneWidths()
	h := m.modalPaneHeight()
	r := m.review
	title := "diff"
	if f := r.selected(); f != nil {
		title = f.Path
	}
	body := m.modalPanes(left, right,
		modalPane{title: countOf(len(r.files), "file"), lines: m.reviewFileLines(island.Inner(left), h),
			focused: !r.diffFocused},
		modalPane{title: layout.Truncate(title, island.Inner(right)), lines: m.reviewDiffLines(h),
			focused: r.diffFocused})
	return m.modalBox(m.reviewTitle(), body, m.reviewKeys())
}

// reviewPaneWidths gives the list under a third, since the diff is what is read.
func (m Model) reviewPaneWidths() (left, right int) {
	inner := island.Inner(m.modalWidth())
	left = inner * 3 / 10
	return left, inner - left
}

// reviewTextWidth is how wide a line of the diff pane may be.
func (m Model) reviewTextWidth() int {
	_, right := m.reviewPaneWidths()
	return island.Inner(right)
}

// reviewTitle names the request, what it changed in all and the comments so far.
func (m Model) reviewTitle() string {
	r := m.review
	title := styleBrand.Render("review") + styleFaint.Render(fmt.Sprintf(" · request %d", r.block.n))
	if r.head == "" {
		title += styleFaint.Render(" · to your files now")
	}
	if len(r.files) > 0 {
		var add, del int
		for _, f := range r.files {
			a, d := changed(f)
			add, del = add+a, del+d
		}
		title += styleFaint.Render(" · ") + styleSafe.Render(fmt.Sprintf("+%d", add)) + " " +
			styleDanger.Render(fmt.Sprintf("−%d", del))
	}
	if rec := m.reviewByID(r.id); rec != nil && len(rec.comments) > 0 {
		title += styleFaint.Render(" · ") + styleBrand.Render(countOf(len(rec.comments), "comment"))
	}
	if r.cut {
		title += styleCaution.Render(" · too large, the last files are missing")
	}
	return title
}

// reviewKeys offers what the cursor is on: the editor's keys while writing.
func (m Model) reviewKeys() string {
	r := m.review
	switch {
	case r.edit != nil:
		return "enter save · " + m.prompt.NewlineKey() + " newline · esc drop it"
	case !r.diffFocused:
		return "↑↓ file · enter diff · ctrl+s send · esc back"
	}
	rows := m.reviewRows()
	if r.line < len(rows) && rows[r.line].comment != nil {
		if r.deleting == rows[r.line].comment.ID {
			return "x again deletes it · any other key keeps it"
		}
		return "c reply · e edit · x delete · ctrl+s send · esc back"
	}
	return "c comment · v range · ]/[ hunk · n/p file · ctrl+s send · esc back"
}

// reviewFileLines lists each changed file with what happened to it.
func (m Model) reviewFileLines(width, height int) []string {
	r := m.review
	switch {
	case r.loading:
		return []string{styleFaint.Render("reading the changes…")}
	case r.err != "":
		return []string{styleDanger.Render(layout.Truncate(r.err, width))}
	case len(r.files) == 0:
		return []string{styleFaint.Render("nothing changed")}
	}
	var out []string
	start, end := listWindow(len(r.files), r.file, height)
	for i := start; i < end; i++ {
		f := r.files[i]
		mark, style := "  ", styleGoal
		if i == r.file {
			mark, style = "▸ ", styleRowCursor
		}
		stat := fileStat(f)
		if n := len(m.fileComments(f.Path)); n > 0 {
			stat += " ✎" + strconv.Itoa(n)
		}
		room := max(1, width-ansi.StringWidth(mark)-2-ansi.StringWidth(stat)-1)
		path := f.Path
		if ansi.StringWidth(path) > room {
			path = ansi.TruncateLeft(path, ansi.StringWidth(path)-room+1, "…")
		}
		out = append(out, style.Render(mark)+changeGlyph(f.Change)+" "+
			style.Render(fmt.Sprintf("%-*s", room, path))+" "+styleFaint.Render(stat))
	}
	return out
}

// reviewDiffLines is the selected file's diff around the cursor, numbered on
// both sides with comments under their lines, or why it is not drawn.
func (m Model) reviewDiffLines(height int) []string {
	r := m.review
	f := r.selected()
	switch {
	case r.loading || r.err != "":
		return nil
	case f == nil:
		return []string{styleFaint.Render("this request left your files as they were")}
	case f.Binary:
		return []string{styleFaint.Render("a binary file, not shown")}
	case f.Cut:
		return []string{styleFaint.Render("too long to show here")}
	case len(f.Hunks) == 0:
		return []string{styleFaint.Render("only its mode changed")}
	}
	width := m.reviewTextWidth()
	var editor []string
	if r.edit != nil {
		editor = m.commentEditor(width)
		height = max(1, height-len(editor))
	}
	rows := m.reviewRows()
	nw := (reviewGutter(*f) - 3) / 2
	num := func(n int) string {
		if n == 0 {
			return strings.Repeat(" ", nw)
		}
		return fmt.Sprintf("%*d", nw, n)
	}
	lo, hi := r.line, r.line
	if r.ranging {
		lo, hi = min(r.anchor, r.line), max(r.anchor, r.line)
	}
	var out []string
	start, end := listWindow(len(rows), r.line, height)
	for i := start; i < end; i++ {
		row := rows[i]
		mark, gutter := " ", styleFaint
		switch {
		case i == r.line && r.diffFocused:
			mark, gutter = "▸", styleRowCursor
		case r.ranging && i >= lo && i <= hi:
			mark, gutter = "┃", styleRowCursor
		}
		h := f.Hunks[row.hunk]
		switch {
		case row.comment != nil:
			out = append(out, gutter.Render(mark)+strings.Repeat(" ", 2*nw+2)+commentLine(row))
		case row.line < 0:
			out = append(out, gutter.Render(mark)+" "+styleFaint.Render(layout.Truncate(h.Header, width-2)))
		default:
			l := h.Lines[row.line]
			out = append(out, gutter.Render(mark+num(l.Old)+" "+num(l.New))+" "+
				lineStyle(l.Op).Render(layout.Truncate(string(rune(l.Op))+l.Text, max(1, width-2*nw-3))))
		}
	}
	return append(out, editor...)
}

// commentLine is one line of a comment: who wrote it, or a line of its words.
func commentLine(row diffRow) string {
	c := row.comment
	bar := styleBrand.Render("┃ ")
	if c.ReplyTo != uuid.Nil {
		bar = styleFaint.Render("┃   ")
	}
	if !row.author {
		return bar + styleGoal.Render(row.text)
	}
	who := "you"
	if c.Author != "" {
		who = c.Author
	}
	label := styleBrand.Render(who)
	if c.Original != "" {
		label += styleFaint.Render(" · edited from a reviewer's")
	}
	return bar + label
}

// commentEditor is the comment being written, under a line saying what it is for.
func (m Model) commentEditor(width int) []string {
	e := m.review.edit
	what := "comment"
	switch {
	case e.op == event.CommentEdited:
		what = "edit"
	case e.target != uuid.Nil:
		what = "reply"
	}
	ta := e.input
	ta.SetWidth(max(8, width-2))
	rule := styleBrand.Render(what) + styleFaint.Render(" "+strings.Repeat("─", max(0, width-len(what)-1)))
	return append([]string{rule}, strings.Split(ta.View(), "\n")...)
}

// changeGlyph marks a file added, deleted or modified, as git status does.
func changeGlyph(c event.FileChange) string {
	switch c {
	case event.FileAdded:
		return styleSafe.Render("A")
	case event.FileDeleted:
		return styleDanger.Render("D")
	case event.FileModified:
	}
	return styleCaution.Render("M")
}

// fileStat is a file's lines added and removed, or why it has none.
func fileStat(f event.FileDiff) string {
	switch {
	case f.Binary:
		return "binary"
	case f.Cut:
		return "too long"
	}
	a, d := changed(f)
	return fmt.Sprintf("+%d −%d", a, d)
}

// changed counts a file's added and removed lines.
func changed(f event.FileDiff) (added, removed int) {
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			switch l.Op {
			case event.LineAdded:
				added++
			case event.LineRemoved:
				removed++
			case event.LineContext:
			}
		}
	}
	return added, removed
}

// reviewGutter is how wide the cursor mark and both line numbers are, with their spaces.
func reviewGutter(f event.FileDiff) int { return 2*len(strconv.Itoa(lastLineNo(f))) + 3 }

// lastLineNo is the highest line number a file's diff shows, for the gutter's width.
func lastLineNo(f event.FileDiff) int {
	n := 1
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			n = max(n, l.Old, l.New)
		}
	}
	return n
}

// lineStyle colours a line as diffs in output are coloured.
func lineStyle(op event.LineOp) lipgloss.Style {
	switch op {
	case event.LineAdded:
		return styleSafe
	case event.LineRemoved:
		return styleDanger
	case event.LineContext:
	}
	return styleGoal
}
