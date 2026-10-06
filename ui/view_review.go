package ui

import (
	"fmt"
	"slices"
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

// reviewTitle names the scope, what it changed in all and the comments so far.
func (m Model) reviewTitle() string {
	r := m.review
	title := styleBrand.Render("review") + styleFaint.Render(" · "+m.scopeLabel())
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
	if m.cur != nil && m.cur.review == r.id && r.id != uuid.Nil {
		title += styleFaint.Render(" · ") + m.spinner.View() + styleFaint.Render(" the reviewer is reading")
	}
	if m.reviewSent() {
		title += styleFaint.Render(" · sent")
	}
	if i := slices.IndexFunc(m.reviews, func(rec *reviewRecord) bool { return rec.id == r.id }); i >= 0 && len(m.reviews) > 1 {
		title += styleFaint.Render(fmt.Sprintf(" · %d of %d", i+1, len(m.reviews)))
	}
	if r.cut {
		title += styleCaution.Render(" · too large, the last files are missing")
	}
	return title
}

// scopeLabel says which changes are shown, from where to where. A review can
// outlive its request, after an undo or in an older session, so none is assumed.
func (m Model) scopeLabel() string {
	r := m.review
	n := "?"
	if r.block != nil {
		n = strconv.Itoa(r.block.n)
	}
	switch r.scope {
	case event.ScopeSession:
		first := "?"
		if b := m.firstReviewable(); b != nil {
			first = strconv.Itoa(b.n)
		}
		return fmt.Sprintf("the session, requests %s to %s", first, n)
	case event.ScopeSince:
		return "your edits since request " + n
	case event.ScopeBranch:
		if r.against == "" {
			return "this branch"
		}
		return "this branch against " + r.against + ", uncommitted too"
	case event.ScopeRequest:
	}
	label := "request " + n
	if r.block == nil {
		label = "a request no longer here"
	}
	if r.head == "" {
		label += " · to your files now"
	}
	return label
}

// readMark says where a reviewer is in the diff: the file it is reading pulses,
// those it has read are dotted, and the rest are blank.
func (m Model) readMark(a *agentState, path string) string {
	switch {
	case a.reading == path && m.pulse/4%2 == 0:
		return toolName.review.Render("◉") + " "
	case a.reading == path:
		return toolName.review.Render("○") + " "
	case a.read[path]:
		return styleFaint.Render("·") + " "
	}
	return "  "
}

// reviewVerdict is the reviewer's summary of the open review, its newest
// comment on no line, nil until it has finished.
func (m Model) reviewVerdict() *event.ReviewComment {
	rec := m.reviewByID(m.review.id)
	if rec == nil {
		return nil
	}
	for i := len(rec.comments) - 1; i >= 0; i-- {
		if c := &rec.comments[i]; c.Path == "" && c.Author != "" && c.ReplyTo == uuid.Nil {
			return c
		}
	}
	return nil
}

// verdictCard is the reviewer's summary above the diff: what it concluded, in
// a few lines, lit for a moment when it lands.
func (m Model) verdictCard(c *event.ReviewComment, width int) []string {
	head := toolName.review
	if m.freshness(c.ID) < 0.35 {
		head = head.Reverse(true)
	}
	out := []string{head.Render("◆ "+c.Author+"'s verdict") + styleFaint.Render(" · "+countOf(m.reviewLineComments(), "comment on lines"))}
	lines := wrapPlain(c.Body, max(8, width-2))
	if len(lines) > maxVerdictLines {
		lines = append(lines[:maxVerdictLines-1], layout.Truncate(lines[maxVerdictLines-1], width-3)+"…")
	}
	for _, l := range lines {
		out = append(out, "  "+styleGoal.Render(l))
	}
	return append(out, styleFaint.Render(strings.Repeat("─", width)))
}

// maxVerdictLines is as much of a verdict as the card shows. The rest is sent with the review.
const maxVerdictLines = 3

// reviewLineComments counts the comments on lines, not the verdict or replies.
func (m Model) reviewLineComments() int {
	n := 0
	if rec := m.reviewByID(m.review.id); rec != nil {
		for _, c := range rec.comments {
			if c.Path != "" && c.ReplyTo == uuid.Nil {
				n++
			}
		}
	}
	return n
}

// reviewKeys offers what the cursor is on: the editor's keys while writing.
func (m Model) reviewKeys() string {
	r := m.review
	switch {
	case r.edit != nil:
		return "enter save · " + m.prompt.NewlineKey() + " newline · esc drop it"
	case r.triage != nil:
		return fmt.Sprintf("comment %d of %d · y keep · n drop · e edit · esc stop", r.triage.at+1, len(r.triage.queue))
	case !r.diffFocused:
		return "↑↓ file · enter diff · s scope · r reviewer · ←→ reviews · ctrl+s send · esc back"
	}
	rows := m.reviewRows()
	if r.line < len(rows) && rows[r.line].comment != nil {
		if r.deleting == rows[r.line].comment.ID {
			return "x again deletes it · any other key keeps it"
		}
		return "c reply · e edit · x delete · t triage · ctrl+s send · esc back"
	}
	return "c comment · v range · ]/[ hunk · n/p file · r reviewer · t triage · ctrl+s send · esc back"
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
	reading := m.reviewer()
	start, end := listWindow(len(r.files), r.file, height)
	for i := start; i < end; i++ {
		f := r.files[i]
		mark, style := "  ", styleGoal
		if i == r.file {
			mark, style = "▸ ", styleRowCursor
		}
		if reading != nil {
			mark += m.readMark(reading, f.Path)
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
		return []string{styleFaint.Render("no changes here")}
	case f.Binary:
		return []string{styleFaint.Render("a binary file, not shown")}
	case f.Cut:
		return []string{styleFaint.Render("too long to show here")}
	case len(f.Hunks) == 0:
		return []string{styleFaint.Render("only its mode changed")}
	}
	width := m.reviewTextWidth()
	var editor, card []string
	if r.edit != nil {
		editor = m.commentEditor(width)
		height = max(1, height-len(editor))
	}
	if v := m.reviewVerdict(); v != nil && height > 8 {
		card = m.verdictCard(v, width)
		height -= len(card)
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
	out := card
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
			out = append(out, gutter.Render(mark)+strings.Repeat(" ", 2*nw+2)+m.commentLine(row))
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
// One that just came in stands out, its name lit then its bar, and settles.
func (m Model) commentLine(row diffRow) string {
	c := row.comment
	fresh := m.freshness(c.ID)
	barStyle, labelStyle := styleBrand, styleBrand
	if fresh < 1 {
		barStyle, labelStyle = toolName.review, toolName.review
	}
	if fresh < 0.35 {
		labelStyle = labelStyle.Reverse(true)
	}
	bar := barStyle.Render("┃ ")
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
	label := labelStyle.Render(who)
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

// reviewRowLine is a review in history: what it reviewed, how many comments it
// has, and a spinner while its reviewer works.
func (m Model) reviewRowLine(mark string, r *historyRow) string {
	b := m.blockByID(r.id)
	label := "review"
	if b != nil {
		label = b.prompt
	}
	icon, state := styleBrand.Render("◆"), ""
	switch {
	case r.running:
		// The spinner says it is still reading, which leaves room for what it reviews.
		icon = m.spinner.View()
	case b != nil && b.end == event.EndAborted:
		state = " · stopped"
	case b != nil && b.end == event.EndError:
		state = " · failed"
	}
	tail := " · " + countOf(r.comments, "comment") + state
	room := max(layout.MinTruncate, m.blockWidth()-lipgloss.Width(mark+icon+" ")-lipgloss.Width(tail))
	return mark + icon + " " + styleGoal.Render(layout.Truncate(label, room)) + styleFaint.Render(tail)
}
