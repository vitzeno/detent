package ui

import (
	"fmt"
	"image/color"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/island"
	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/syntax"
)

// The review's box: the changed files on the left, the selected one's diff on
// the right with its comments under the lines they are about.

// commentEditorLines is how tall the comment being written is drawn.
const commentEditorLines = 3

// maxVerdictLines is as much of a verdict as the card shows. The rest is sent with the review.
const maxVerdictLines = 3

// splitMin is the narrowest diff pane the split view draws in, each side then
// keeping room for its numbers and a readable stretch of code.
const splitMin = 90

// splitRow is one line of the split view: a line on each side, or a header or
// comment across both. Each is an index into reviewRows, -1 for none.
type splitRow struct{ left, right, across int }

// hunkKey names one hunk of one file of the diff shown.
type hunkKey struct{ file, hunk int }

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
	if n := len(m.viewed[r.id]); n > 0 && len(r.files) > 0 {
		title += styleFaint.Render(fmt.Sprintf(" · %d/%d viewed", n, len(r.files)))
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

// reviewKeys offers what the cursor is on, leading with what esc will close: it
// closes a layer at a time, and the line is cut to the box, which hid "esc back".
func (m Model) reviewKeys() string {
	r := m.review
	switch {
	case r.edit != nil:
		return "esc drops the draft · enter save · " + m.prompt.NewlineKey() + " newline"
	case r.triage != nil:
		return fmt.Sprintf("esc stops triage · comment %d of %d · y keep · n drop · e edit",
			r.triage.at+1, len(r.triage.queue))
	case r.ranging:
		return "esc drops the range · c comments on it · ↑↓ extend it"
	case !r.diffFocused:
		return "esc closes · ↑↓ file · enter diff · space viewed · s scope · r reviewer · ←→ reviews · ctrl+s send"
	}
	rows := m.reviewRows()
	if r.line < len(rows) && rows[r.line].comment != nil {
		if r.deleting == rows[r.line].comment.ID {
			return "x again deletes it · any other key keeps it"
		}
		return "esc closes · c reply · e edit · x delete · </> comments · t triage · ctrl+s send"
	}
	return "esc closes · c comment · v range · ]/[ hunk · </> comments · n/p file · space viewed · w split · r reviewer · t triage · ctrl+s send"
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
		// A file marked viewed steps back, the way a reviewed file folds away.
		if m.viewed[r.id][f.Path] {
			mark += styleSafe.Render("✓") + " "
			if i != r.file {
				style = styleFaint
			}
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
	// marked is row i's cursor or range mark, and the gutter style to draw it in.
	marked := func(i int) (string, lipgloss.Style) {
		switch {
		case i == r.line && r.diffFocused:
			return "▸", styleRowCursor
		case r.ranging && i >= lo && i <= hi:
			return "┃", styleRowCursor
		}
		return " ", styleFaint
	}
	// inline is row i as one line across the pane: how every row is drawn
	// inline, and a header or comment is in the split view too.
	inline := func(i int) string {
		row := rows[i]
		mark, gutter := marked(i)
		h := f.Hunks[row.hunk]
		switch {
		case row.comment != nil:
			return gutter.Render(mark) + strings.Repeat(" ", 2*nw+2) + m.commentLine(row)
		case row.line < 0:
			return gutter.Render(mark) + " " + styleFaint.Render(layout.Truncate(h.Header, width-2))
		}
		l := h.Lines[row.line]
		return gutter.Render(mark+num(l.Old)+" "+num(l.New)) + " " + m.codeLine(row.hunk, row.line, max(1, width-2*nw-3))
	}
	out := card
	if r.split && m.splitFits() {
		out = append(out, m.splitLines(*f, rows, height, width, nw, marked, inline)...)
		return append(out, editor...)
	}
	start, end := listWindow(len(rows), r.line, height)
	for i := start; i < end; i++ {
		out = append(out, inline(i))
	}
	return append(out, editor...)
}

func (m Model) splitFits() bool { return m.reviewTextWidth() >= splitMin }

// pairRows lays rows side by side: each run of removed lines against the added
// lines that replaced it, a pair to a line, with any comment on them after the run.
func pairRows(rows []diffRow, f event.FileDiff) []splitRow {
	var out []splitRow
	var removed, added, comments []int
	flush := func() {
		for k := range max(len(removed), len(added)) {
			s := splitRow{left: -1, right: -1, across: -1}
			if k < len(removed) {
				s.left = removed[k]
			}
			if k < len(added) {
				s.right = added[k]
			}
			out = append(out, s)
		}
		for _, c := range comments {
			out = append(out, splitRow{left: -1, right: -1, across: c})
		}
		removed, added, comments = nil, nil, nil
	}
	for i, row := range rows {
		switch {
		case row.comment != nil && len(removed)+len(added) > 0:
			comments = append(comments, i)
			continue
		case row.comment != nil || row.line < 0:
			flush()
			out = append(out, splitRow{left: -1, right: -1, across: i})
			continue
		}
		switch f.Hunks[row.hunk].Lines[row.line].Op {
		case event.LineRemoved:
			if len(added) > 0 {
				flush()
			}
			removed = append(removed, i)
		case event.LineAdded:
			added = append(added, i)
		case event.LineContext:
			flush()
			out = append(out, splitRow{left: i, right: i, across: -1})
		}
	}
	flush()
	return out
}

// splitLines draws the diff side by side, the old file on the left and the new
// on the right, windowed round the line the cursor is on.
func (m Model) splitLines(f event.FileDiff, rows []diffRow, height, width, nw int,
	marked func(int) (string, lipgloss.Style), inline func(int) string) []string {
	pairs := pairRows(rows, f)
	at := slices.IndexFunc(pairs, func(p splitRow) bool {
		return p.left == m.review.line || p.right == m.review.line || p.across == m.review.line
	})
	half := (width - 3) / 2
	cell := func(i int, old bool) string {
		if i < 0 {
			return strings.Repeat(" ", half)
		}
		l := f.Hunks[rows[i].hunk].Lines[rows[i].line]
		n := l.New
		if old {
			n = l.Old
		}
		mark, gutter := marked(i)
		s := gutter.Render(mark+fmt.Sprintf("%*s", nw, lineNo(n))) + " " + m.codeLine(rows[i].hunk, rows[i].line, max(1, half-nw-2))
		return s + strings.Repeat(" ", max(0, half-lipgloss.Width(s)))
	}
	var out []string
	start, end := listWindow(len(pairs), max(at, 0), height)
	for _, p := range pairs[start:end] {
		if p.across >= 0 {
			out = append(out, inline(p.across))
			continue
		}
		out = append(out, cell(p.left, true)+styleFaint.Render(" │ ")+cell(p.right, false))
	}
	return out
}

// codeLine is a line of the selected file coloured by language, an added or removed
// one tinted the width of it. A file no lexer knows is drawn as before.
func (m Model) codeLine(hunk, line, width int) string {
	f := m.review.selected()
	l := f.Hunks[hunk].Lines[line]
	code := m.codeOf(hunk)
	if code == nil {
		return lineStyle(l.Op).Render(layout.Truncate(string(rune(l.Op))+l.Text, width))
	}
	marker, tint := " ", color.Color(nil)
	switch l.Op {
	case event.LineAdded:
		marker, tint = sgr(38, palette.Safe)+"+"+"\x1b[39m", palette.DiffAdded
	case event.LineRemoved:
		marker, tint = sgr(38, palette.Danger)+"-"+"\x1b[39m", palette.DiffRemoved
	case event.LineContext:
	}
	// Not layout.Truncate, which defuses: the text was defused as the diff arrived,
	// and all the colouring added is its own colour, which must reach the terminal.
	body := marker + ansi.Truncate(code[line], max(1, width-1), "…") + "\x1b[22m\x1b[39m"
	if tint == nil {
		return body
	}
	pad := strings.Repeat(" ", max(0, width-ansi.StringWidth(body)))
	return sgr(48, tint) + body + pad + "\x1b[49m"
}

// codeOf is a hunk of the selected file coloured by language, made once and
// kept, nil when no lexer knows the file.
func (m Model) codeOf(hunk int) []string {
	r := m.review
	key := hunkKey{file: r.file, hunk: hunk}
	if code, ok := r.code[key]; ok {
		return code
	}
	f := r.selected()
	texts := make([]string, len(f.Hunks[hunk].Lines))
	for i, l := range f.Hunks[hunk].Lines {
		texts[i] = l.Text
	}
	code := syntax.Hunk(f.Path, texts, palette.Syntax)
	if r.code != nil {
		r.code[key] = code
	}
	return code
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
	rule := styleBrand.Render(what) + styleFaint.Render(" "+strings.Repeat("─", max(0, width-len(what)-1)))
	return append([]string{rule}, strings.Split(e.input.View(), "\n")...)
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
	out := []string{head.Render("◆ "+c.Author+"'s verdict") + styleFaint.Render(" · "+countOf(m.reviewLineComments(), "comment")+" on lines")}
	lines := wrapPlain(c.Body, max(8, width-2))
	if len(lines) > maxVerdictLines {
		lines = append(lines[:maxVerdictLines-1], layout.Truncate(lines[maxVerdictLines-1], width-3)+"…")
	}
	for _, l := range lines {
		out = append(out, "  "+styleGoal.Render(l))
	}
	return append(out, styleFaint.Render(strings.Repeat("─", width)))
}

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

// reviewSummary is a review as the output pane shows it: what it reviews, how
// its reviewer stands, its verdict and every comment by where it is.
func (m Model) reviewSummary(id uuid.UUID) []string {
	width := paneInner(m.layout.outputColW)
	rec := m.reviewByID(id)
	label := "review"
	for _, b := range m.blocks {
		if b.review == id {
			label = b.prompt
		}
	}
	out := []string{toolName.review.Render("◆ ") + styleGoal.Render(layout.Truncate(label, width-2))}
	var reviewer *agentState
	for _, a := range m.agentOrder {
		if rv := reviewerOf(a); rv != nil && rv.review == id {
			reviewer = a
		}
	}
	switch {
	case reviewer != nil && !reviewer.ended:
		// No spinner: this pane redraws on facts, not ticks, so one would sit still.
		state := fmt.Sprintf("the reviewer is reading · %d/%d files", len(reviewer.read), reviewerOf(reviewer).files)
		if reviewer.reading != "" {
			state += " · now " + reviewer.reading
		}
		out = append(out, toolName.review.Render("◇")+" "+styleMuted.Render(layout.Truncate(state, width-3)))
	case reviewer != nil:
		out = append(out, m.agentGlyph(reviewer)+" "+styleMuted.Render("reviewer: "+m.endedDetail(reviewer)))
	}
	if rec == nil {
		return append(out, "", styleFaint.Render("no comments yet"))
	}
	var lines []event.ReviewComment
	for _, c := range rec.comments {
		switch {
		case c.Path == "" && c.Author != "" && c.ReplyTo == uuid.Nil:
			out = append(out, "", styleBrand.Render("verdict"))
			for _, l := range wrapPlain(c.Body, max(8, width-2)) {
				out = append(out, "  "+styleGoal.Render(l))
			}
		case c.Path != "" && c.ReplyTo == uuid.Nil:
			lines = append(lines, c)
		}
	}
	out = append(out, "", styleBrand.Render(countOf(len(lines), "comment")+" on lines"))
	for _, c := range lines {
		who := "you"
		if c.Author != "" {
			who = c.Author
		}
		at := fmt.Sprintf("%s:%d", c.Path, c.Start)
		first, _, _ := strings.Cut(c.Body, "\n")
		out = append(out, "  "+styleMuted.Render(at)+" "+styleFaint.Render(who)+" "+
			styleGoal.Render(layout.Truncate(first, max(8, width-lipgloss.Width(at+who)-5))))
	}
	if rec.submitted {
		out = append(out, "", styleFaint.Render("sent to the agent"))
	}
	return append(out, "", styleFaint.Render("enter or /review opens it"))
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

// lineNo is a line number, blank on the side a line is not on.
func lineNo(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// sgr is the escape setting a foreground (38) or background (48) to c.
func sgr(code int, c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", code, r>>8, g>>8, b>>8)
}
