package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/termsafe"
)

// The review modal: what one request changed in the human's files, read
// between the checkpoints taken as it began and as it ended, and commented on.

// reviewRecord is one review's comments, folded from ReviewCommented. Open
// until submitted, then read-only, and the next comment starts another.
type reviewRecord struct {
	id, reviewed uuid.UUID
	base, head   string
	comments     []event.ReviewComment
	submitted    bool
}

// diffRow is one line the diff pane draws: a hunk's header when line is -1,
// and when comment is set, its author's line or one line of its words.
type diffRow struct {
	hunk, line int
	comment    *event.ReviewComment
	author     bool
	text       string
}

// commentEdit is a comment being written: a new one over rows from to to,
// a reply to target, or target's body rewritten.
type commentEdit struct {
	op       event.CommentOp
	target   uuid.UUID
	from, to int
	input    textarea.Model
}

// showReview is /review: the last request that checkpointed the files, or request N.
func (m Model) showReview(input string) (Model, tea.Cmd) {
	b, why := m.reviewTarget(strings.TrimSpace(strings.TrimPrefix(input, "/review")))
	if b == nil {
		m.noteErr(why)
		return m, nil
	}
	m.review = reviewState{block: b, base: b.base, head: m.reviewHead(b), loading: true, back: m.nav.focus}
	m.review.id = m.openReviewOf(b.id, m.review.base, m.review.head)
	m.mode = modeReview
	return m, m.send(event.LoadDiff{Base: m.review.base, Head: m.review.head})
}

// reviewTarget is the request /review means, or why there is none.
func (m Model) reviewTarget(arg string) (*turnBlock, string) {
	if arg == "" {
		for _, b := range slices.Backward(m.blocks) {
			if b.base != "" {
				return b, ""
			}
		}
		return nil, "nothing to review: no request has checkpointed your files, which needs a git work tree"
	}
	n, err := strconv.Atoi(strings.TrimPrefix(arg, "#"))
	if err != nil {
		return nil, "/review takes a request's number, e.g. /review 3"
	}
	for _, b := range m.blocks {
		if b.n != n || b.userCommands || b.seam != nil {
			continue
		}
		if b.base == "" {
			return nil, fmt.Sprintf("request %d has no checkpoint of your files to review", n)
		}
		return b, ""
	}
	return nil, fmt.Sprintf("there is no request %d", n)
}

// reviewHead is where the request left the files: its own end, else for one
// stored before that was recorded the next request's start, else the files now.
func (m Model) reviewHead(b *turnBlock) string {
	if b.tree != "" || !b.ended {
		return b.tree
	}
	for _, next := range m.blocks[slices.Index(m.blocks, b)+1:] {
		if next.base != "" {
			return next.base
		}
	}
	return ""
}

// openReviewOf is the open review of these changes, or a new id when none is.
// Minted here, so a second comment sent before the first is recorded joins it.
func (m Model) openReviewOf(reviewed uuid.UUID, base, head string) uuid.UUID {
	for _, r := range slices.Backward(m.reviews) {
		if r.reviewed == reviewed && r.base == base && r.head == head && !r.submitted {
			return r.id
		}
	}
	return uuid.Must(uuid.NewV7())
}

// diffLoaded fills the review it answers, defusing what the files say once, here.
func (m *Model) diffLoaded(v event.DiffLoaded) {
	r := &m.review
	if !r.loading || v.Base != r.base || v.Head != r.head {
		return
	}
	r.loading, r.cut, r.err = false, v.Cut, v.Err
	r.files = defused(v.Files)
}

// reviewCommented folds one comment into its review, made on its first.
func (m *Model) reviewCommented(v event.ReviewCommented) {
	rec := m.reviewByID(v.Review)
	if rec == nil {
		rec = &reviewRecord{id: v.Review, reviewed: v.Reviewed, base: v.Base, head: v.Head}
		m.reviews = append(m.reviews, rec)
	}
	c := defusedComment(v.Comment)
	switch v.Op {
	case event.CommentAdded:
		rec.comments = append(rec.comments, c)
	case event.CommentEdited:
		if i := slices.IndexFunc(rec.comments, func(o event.ReviewComment) bool { return o.ID == c.ID }); i >= 0 {
			rec.comments[i] = c
		}
	case event.CommentDeleted:
		// Its replies answer nothing once it is gone.
		rec.comments = slices.DeleteFunc(rec.comments, func(o event.ReviewComment) bool {
			return o.ID == c.ID || o.ReplyTo == c.ID
		})
	}
}

// reviewSubmitted closes a review, and the modal if it was on it.
func (m *Model) reviewSubmitted(v event.ReviewSubmitted) {
	rec := m.reviewByID(v.Review)
	if rec == nil {
		return
	}
	rec.submitted = true
	if m.mode == modeReview && m.review.id == v.Review {
		*m, _ = m.closeReview()
		m.noteOK(fmt.Sprintf("sent %s to the agent", countOf(v.Comments, "comment")))
	}
}

func (m Model) reviewByID(id uuid.UUID) *reviewRecord {
	for _, r := range m.reviews {
		if r.id == id {
			return r
		}
	}
	return nil
}

// closeReview returns to the pane the review was opened from.
func (m Model) closeReview() (Model, tea.Cmd) {
	back := m.review.back
	m.review = reviewState{}
	m.backToInput()
	if m.mode == modeInput && back != focusInput {
		m.nav.focus = back
		m.prompt.Blur()
	}
	return m, nil
}

// reviewKey owns every key while the review is open, the editor's first.
func (m Model) reviewKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	r := &m.review
	if r.edit != nil {
		return m.editKey(msg)
	}
	key := msg.String()
	if key != "x" {
		r.deleting = uuid.Nil
	}
	switch key {
	case "esc":
		if r.ranging {
			r.ranging = false
			return m, nil
		}
		return m.closeReview()
	case "tab":
		r.diffFocused = !r.diffFocused
	case "enter":
		r.diffFocused = true
	case "up", "k":
		m.reviewMove(-1)
	case "down", "j":
		m.reviewMove(1)
	case "pgup":
		m.reviewMove(-m.modalPaneHeight())
	case "pgdown":
		m.reviewMove(m.modalPaneHeight())
	case "n":
		r.pickFile(r.file + 1)
	case "p":
		r.pickFile(r.file - 1)
	case "]":
		r.line = m.nextHunk(1)
	case "[":
		r.line = m.nextHunk(-1)
	case "v":
		m.toggleRange()
	case "c":
		return m.startComment()
	case "e":
		return m.startEdit()
	case "x":
		return m.deleteComment()
	case "ctrl+s":
		return m.submitReview()
	}
	return m, nil
}

// reviewMove steps the line in the diff pane, or the file in the list.
func (m *Model) reviewMove(d int) {
	r := &m.review
	if !r.diffFocused {
		r.pickFile(r.file + d)
		return
	}
	r.line = min(max(r.line+d, 0), max(0, len(m.reviewRows())-1))
}

// pickFile selects file i, from its first line.
func (r *reviewState) pickFile(i int) {
	r.file, r.line, r.ranging = min(max(i, 0), max(0, len(r.files)-1)), 0, false
}

// selected is the file under the cursor, nil while there is none.
func (r *reviewState) selected() *event.FileDiff {
	if r.file >= len(r.files) {
		return nil
	}
	return &r.files[r.file]
}

// reviewRows is the selected file as the diff pane lists it: each hunk's header,
// its lines, and under the line a comment ends on, the comment and its replies.
func (m Model) reviewRows() []diffRow {
	f := m.review.selected()
	if f == nil {
		return nil
	}
	comments := m.fileComments(f.Path)
	// What the gutter and a reply's indent leave of the pane.
	width := max(8, m.reviewTextWidth()-reviewGutter(*f)-4)
	var out []diffRow
	for h, hunk := range f.Hunks {
		out = append(out, diffRow{hunk: h, line: -1})
		for l, line := range hunk.Lines {
			out = append(out, diffRow{hunk: h, line: l})
			for i := range comments {
				c := &comments[i]
				if c.ReplyTo == uuid.Nil && endsOn(*c, line) {
					out = append(out, thread(c, comments, h, width)...)
				}
			}
		}
	}
	return out
}

// thread is a comment's rows and then its replies', each wrapped to width.
func thread(c *event.ReviewComment, all []event.ReviewComment, hunk, width int) []diffRow {
	var out []diffRow
	add := func(c *event.ReviewComment) {
		out = append(out, diffRow{hunk: hunk, line: -1, comment: c, author: true})
		for _, t := range wrapPlain(c.Body, width) {
			out = append(out, diffRow{hunk: hunk, line: -1, comment: c, text: t})
		}
	}
	add(c)
	for i := range all {
		if all[i].ReplyTo == c.ID {
			add(&all[i])
		}
	}
	return out
}

// endsOn is whether line is the last a comment covers, on the side it numbers.
func endsOn(c event.ReviewComment, l event.DiffLine) bool {
	if c.Side == "old" {
		return l.Op == event.LineRemoved && l.Old == c.End
	}
	return l.New != 0 && l.New == c.End
}

// fileComments is the open review's comments on path.
func (m Model) fileComments(path string) []event.ReviewComment {
	rec := m.reviewByID(m.review.id)
	if rec == nil {
		return nil
	}
	var out []event.ReviewComment
	for _, c := range rec.comments {
		if c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

// nextHunk is the row of the next hunk's header in direction d, or the line
// unmoved when there is none that way.
func (m Model) nextHunk(d int) int {
	rows := m.reviewRows()
	r := m.review
	for i := r.line + d; i >= 0 && i < len(rows); i += d {
		if rows[i].line < 0 && rows[i].comment == nil {
			return i
		}
	}
	return r.line
}

// toggleRange starts a range of lines at the cursor, or drops one.
func (m *Model) toggleRange() {
	r := &m.review
	rows := m.reviewRows()
	if r.ranging || r.line >= len(rows) || rows[r.line].line < 0 {
		r.ranging = false
		return
	}
	r.ranging, r.anchor = true, r.line
}

// startComment opens the editor: a reply on a comment, else a new comment
// on the range or the line under the cursor.
func (m Model) startComment() (Model, tea.Cmd) {
	r := &m.review
	rows := m.reviewRows()
	if !r.diffFocused || r.line >= len(rows) {
		return m, nil
	}
	if c := rows[r.line].comment; c != nil {
		root := c.ID
		if c.ReplyTo != uuid.Nil {
			root = c.ReplyTo
		}
		r.edit = newCommentEdit(event.CommentAdded, root, "")
		return m, nil
	}
	from, to := r.line, r.line
	if r.ranging {
		from, to = min(r.anchor, r.line), max(r.anchor, r.line)
	}
	if rows[from].line < 0 || rows[from].hunk != rows[to].hunk {
		m.noteErr("a comment covers lines of one hunk")
		return m, nil
	}
	r.edit = newCommentEdit(event.CommentAdded, uuid.Nil, "")
	r.edit.from, r.edit.to = from, to
	return m, nil
}

// startEdit opens the editor on the comment under the cursor, its words in it.
func (m Model) startEdit() (Model, tea.Cmd) {
	rows := m.reviewRows()
	if m.review.line >= len(rows) || rows[m.review.line].comment == nil {
		return m, nil
	}
	c := rows[m.review.line].comment
	m.review.edit = newCommentEdit(event.CommentEdited, c.ID, c.Body)
	return m, nil
}

// deleteComment deletes the comment under the cursor on a second x, since a
// reviewer's comment, once gone, cannot be written again.
func (m Model) deleteComment() (Model, tea.Cmd) {
	r := &m.review
	rows := m.reviewRows()
	if r.line >= len(rows) || rows[r.line].comment == nil {
		return m, nil
	}
	c := rows[r.line].comment
	if r.deleting != c.ID {
		r.deleting = c.ID
		return m, nil
	}
	r.deleting = uuid.Nil
	r.line = max(0, r.line-1)
	return m, m.sendComment(event.CommentDeleted, event.ReviewComment{ID: c.ID})
}

func newCommentEdit(op event.CommentOp, target uuid.UUID, body string) *commentEdit {
	ta := textarea.New()
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetKeys("shift+enter", "alt+enter", "ctrl+j")
	ta.SetHeight(commentEditorLines)
	applyInputTheme(&ta)
	ta.SetValue(body)
	ta.Focus()
	return &commentEdit{op: op, target: target, input: ta}
}

// editKey writes into the editor: enter saves, esc drops the draft.
func (m Model) editKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	r := &m.review
	switch msg.String() {
	case "esc":
		r.edit = nil
		return m, nil
	case "enter":
		e := r.edit
		r.edit, r.ranging = nil, false
		body := strings.TrimSpace(e.input.Value())
		if body == "" {
			return m, nil
		}
		return m, m.saveComment(e, body)
	}
	var cmd tea.Cmd
	r.edit.input, cmd = r.edit.input.Update(msg)
	return m, cmd
}

// saveComment sends what the editor holds as the comment it was opened for.
func (m Model) saveComment(e *commentEdit, body string) tea.Cmd {
	if e.op == event.CommentEdited {
		c := m.commentByID(e.target)
		if c == nil {
			return nil
		}
		// A reviewer's comment the human rewrites is theirs, its first words kept.
		edited := *c
		if edited.Author != "" {
			edited.Original, edited.Author = edited.Body, ""
		}
		edited.Body = body
		return m.sendComment(event.CommentEdited, edited)
	}
	c := event.ReviewComment{ID: uuid.Must(uuid.NewV7()), Body: body, ReplyTo: e.target}
	if parent := m.commentByID(e.target); parent != nil {
		c.Path, c.Side, c.Start, c.End = parent.Path, parent.Side, parent.Start, parent.End
		return m.sendComment(event.CommentAdded, c)
	}
	m.anchor(&c, e.from, e.to)
	return m.sendComment(event.CommentAdded, c)
}

// anchor places c on rows from to to: the new side's numbers, unless every
// line was removed, with the lines quoted as the diff shows them.
func (m Model) anchor(c *event.ReviewComment, from, to int) {
	f := m.review.selected()
	rows := m.reviewRows()
	var lines []event.DiffLine
	for _, row := range rows[from : to+1] {
		if row.line >= 0 && row.comment == nil {
			lines = append(lines, f.Hunks[row.hunk].Lines[row.line])
		}
	}
	c.Path, c.Side = f.Path, "old"
	if slices.ContainsFunc(lines, func(l event.DiffLine) bool { return l.Op != event.LineRemoved }) {
		c.Side = "new"
	}
	quote := make([]string, 0, len(lines))
	for _, l := range lines {
		quote = append(quote, string(rune(l.Op))+l.Text)
		n := l.New
		if c.Side == "old" {
			n = l.Old
		}
		if n == 0 {
			continue
		}
		if c.Start == 0 {
			c.Start = n
		}
		c.End = n
	}
	c.Quote = strings.Join(quote, "\n")
}

func (m Model) commentByID(id uuid.UUID) *event.ReviewComment {
	rec := m.reviewByID(m.review.id)
	if rec == nil || id == uuid.Nil {
		return nil
	}
	for i := range rec.comments {
		if rec.comments[i].ID == id {
			return &rec.comments[i]
		}
	}
	return nil
}

func (m Model) sendComment(op event.CommentOp, c event.ReviewComment) tea.Cmd {
	r := m.review
	return m.send(event.CommentReview{Review: r.id, Reviewed: r.block.id, Base: r.base, Head: r.head,
		Op: op, Comment: c})
}

// submitReview sends the review's comments to the agent as its next prompt.
func (m Model) submitReview() (Model, tea.Cmd) {
	rec := m.reviewByID(m.review.id)
	if rec == nil || len(rec.comments) == 0 {
		m.noteErr("nothing to send: c comments on the line under the cursor")
		return m, nil
	}
	return m, m.send(event.SubmitReview{Review: rec.id, Request: m.review.block.n,
		Comments: slices.Clone(rec.comments)})
}

// defused is files with every path and line made safe to print. A copy, since
// an event is shared, and a CRLF line loses its \r rather than showing it.
func defused(files []event.FileDiff) []event.FileDiff {
	out := make([]event.FileDiff, len(files))
	for i, f := range files {
		f.Path = termsafe.Printable(f.Path)
		f.Hunks = slices.Clone(f.Hunks)
		for j, h := range f.Hunks {
			h.Header = termsafe.Printable(h.Header)
			h.Lines = slices.Clone(h.Lines)
			for k, l := range h.Lines {
				h.Lines[k].Text = termsafe.Printable(strings.TrimSuffix(l.Text, "\r"))
			}
			f.Hunks[j] = h
		}
		out[i] = f
	}
	return out
}

// defusedComment is c safe to print, since a reviewer's words are a model's.
func defusedComment(c event.ReviewComment) event.ReviewComment {
	c.Author = termsafe.Printable(c.Author)
	c.Path = termsafe.Printable(c.Path)
	c.Quote = termsafe.Printable(c.Quote)
	c.Body = termsafe.Printable(c.Body)
	c.Original = termsafe.Printable(c.Original)
	return c
}
