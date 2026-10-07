package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/vitzeno/detent/defuse"
	"github.com/vitzeno/detent/event"
)

// The review modal: what one request changed in the human's files, read
// between the checkpoints taken as it began and as it ended, and commented on.

// reviewScopes is the order s cycles through them.
var reviewScopes = []event.ReviewScope{event.ScopeRequest, event.ScopeSession, event.ScopeSince, event.ScopeBranch}

// fadeFor is how long a comment that just came in stands out.
const fadeFor = 900 * time.Millisecond

// reviewRecord is one review's comments, folded from ReviewCommented. Open
// until submitted, then read-only, and the next comment starts another.
type reviewRecord struct {
	// arrived is when each comment came in live, which it is highlighted for a moment after.
	arrived      map[uuid.UUID]time.Time
	id, reviewed uuid.UUID
	scope        event.ReviewScope
	against      string
	base, head   string
	comments     []event.ReviewComment
	submitted    bool
}

// reviewModal is the review: one request's changes, the file selected
// and the line in it, and which pane the arrows move.
type reviewModal struct {
	// block is the request the review belongs to, the last one for a wider
	// scope and none for a branch, and request the one request scope shows.
	block, request *turnBlock
	scope          event.ReviewScope
	// against is the ref a branch is compared with, "" until named or known.
	against string
	// raw is files as the endpoint should read them, before defusing for the screen.
	raw []event.FileDiff
	// pinned keeps the id of a review opened by name when its diff arrives.
	pinned bool
	// stepBack is set while /review with no number looks for a request that changed something.
	stepBack   bool
	base, head string
	// id is the review comments go to, open or about to be.
	id         uuid.UUID
	loading    bool
	files      []event.FileDiff
	cut        bool
	err        string
	file, line int
	// diffFocused is whether the arrows move the line rather than the file.
	diffFocused bool
	// ranging is a v range running from anchor to the line.
	ranging bool
	anchor  int
	edit    *commentEdit
	// deleting is the comment x was pressed on once, deleted on the second.
	deleting uuid.UUID
	// triage walks the reviewer's comments one at a time, nil when not.
	triage *triageState
	// split draws the diff side by side, when the pane is wide enough.
	split bool
	// code is each hunk's lines coloured by language, filled as hunks are drawn
	// and kept for the diff it was made from: a map, so a copy of Model shares it.
	code map[hunkKey][]string
	// back is the pane it was opened from, which esc returns to.
	back focusPane
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

// triageState is a walk through the reviewer's comments: those left to go,
// where it is, and what was kept and dropped.
type triageState struct {
	queue         []uuid.UUID
	at            int
	kept, dropped int
}

// fadeMsg redraws a comment still fading.
type fadeMsg struct{}

// showReview is /review: the last request that checkpointed the files, request
// N, the session, your edits since, or the branch against main or a ref.
func (m Model) showReview(input string) (Model, tea.Cmd) {
	arg := strings.TrimSpace(strings.TrimPrefix(input, "/review"))
	word, ref, _ := strings.Cut(arg, " ")
	scope := event.ScopeRequest
	switch word {
	case "session", "since", "branch":
		scope, arg = event.ReviewScope(word), ""
	}
	// A reviewer at work is what /review alone most likely means.
	if arg == "" && m.cur != nil && m.cur.review != uuid.Nil {
		if rec := m.reviewByID(m.cur.review); rec != nil {
			return m.openReview(rec)
		}
	}
	b, why := m.reviewTarget(arg)
	if b == nil && (scope != event.ScopeBranch || m.run.Commit == "") {
		m.noteErr(why)
		return m, nil
	}
	r := &reviewModal{block: b, back: m.nav.focus, against: strings.TrimSpace(ref), stepBack: arg == "" && word == ""}
	m.openModal(r)
	if !r.scopeOpen(m, scope) {
		m.noteErr(fmt.Sprintf("no %s to review yet", scopeNoun(scope)))
		scope = event.ScopeRequest
	}
	r.load(&m, scope)
	tick := m.fadeTick()
	return m, tick
}

// reviewTarget is the request /review means, or why there is none.
func (m Model) reviewTarget(arg string) (*turnBlock, string) {
	if arg == "" {
		if b := m.lastReviewable(false); b != nil {
			return b, ""
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

// openReview shows a recorded review: its own scope and trees, and its id
// kept whatever a branch's base has become.
func (m Model) openReview(rec *reviewRecord) (Model, tea.Cmd) {
	back := m.nav.focus
	// Stepping to another review keeps the pane the first was opened from.
	if was := modalAs[*reviewModal](m); was != nil {
		back = was.back
	}
	blk := m.blockByID(rec.reviewed)
	m.openModal(&reviewModal{block: blk, request: blk, scope: rec.scope, base: rec.base, head: rec.head,
		against: rec.against, id: rec.id, pinned: true, loading: true, back: back})
	if rec.scope == event.ScopeBranch {
		m.send(event.LoadDiff{Branch: true, Against: rec.against})
	} else {
		m.send(event.LoadDiff{Base: rec.base, Head: rec.head})
	}
	// A comment that came in while it was shut may still be fading.
	tick := m.fadeTick()
	return m, tick
}

// openReviewRow opens the review a history row stands for.
func (m Model) openReviewRow(r *historyRow) (Model, tea.Cmd) {
	if rec := m.reviewByID(r.review); rec != nil {
		return m.openReview(rec)
	}
	m.noteOK("the reviewer has not commented yet")
	return m, nil
}

// key takes every key while the review is open, the editor's first.
func (r *reviewModal) key(m *Model, msg tea.KeyPressMsg) tea.Cmd {
	if r.edit != nil {
		return r.editKey(m, msg)
	}
	k := keymap.review
	if r.triage != nil && r.triageKey(m, msg) {
		return nil
	}
	if !key.Matches(msg, k.remove) {
		r.deleting = uuid.Nil
	}
	if key.Matches(msg, k.comment, k.edit, k.remove, k.send, k.reviewer, k.triage) && r.sent(*m) {
		m.noteErr("this review was sent: s or /review starts another")
		return nil
	}
	switch {
	case key.Matches(msg, k.close):
		if r.ranging {
			r.ranging = false
			return nil
		}
		r.close(m)
	case key.Matches(msg, k.pane):
		r.diffFocused = !r.diffFocused
	case key.Matches(msg, k.diff):
		r.diffFocused = true
	case key.Matches(msg, k.file):
		r.pickFile(r.file + 1)
	case key.Matches(msg, k.prevFile):
		r.pickFile(r.file - 1)
	case key.Matches(msg, k.hunk):
		r.line = r.nextHunk(*m, 1)
	case key.Matches(msg, k.prevHunk):
		r.line = r.nextHunk(*m, -1)
	case key.Matches(msg, k.rng):
		r.toggleRange(*m)
	case key.Matches(msg, k.comment):
		r.startComment(m)
	case key.Matches(msg, k.edit):
		r.startEdit(*m)
	case key.Matches(msg, k.remove):
		r.deleteComment(*m)
	case key.Matches(msg, k.send):
		r.submit(m)
	case key.Matches(msg, k.scope):
		r.nextScope(m)
	case key.Matches(msg, k.reviewer):
		r.startReviewer(m)
	case key.Matches(msg, k.prevReview):
		r.step(m, -1)
	case key.Matches(msg, k.nextReview):
		r.step(m, 1)
	case key.Matches(msg, k.triage):
		r.startTriage(m)
	case key.Matches(msg, k.prevComment):
		r.jumpComment(m, -1)
	case key.Matches(msg, k.nextComment):
		r.jumpComment(m, 1)
	case key.Matches(msg, k.viewed):
		r.toggleViewed(m)
	case key.Matches(msg, k.split):
		if !m.splitFits() {
			m.noteErr(fmt.Sprintf("too narrow to split: the diff needs %d columns", splitMin))
			break
		}
		r.split = !r.split
	default:
		if d, ok := k.move.delta(msg, m.modalPaneHeight()); ok {
			r.move(m, d)
		}
	}
	return nil
}

// sync sizes the editor where it is laid out, so typing wraps at the width shown.
func (r *reviewModal) sync(m *Model) {
	if e := r.edit; e != nil {
		e.input.SetWidth(max(8, m.reviewTextWidth()-2))
	}
}

// hint leads with what esc will do in the review, its other keys being in the box.
func (r *reviewModal) hint(m Model) string {
	return barLine(r.esc(m), note("the review's keys are in the box"))
}

func (r *reviewModal) close(m *Model) { m.closeModal(r.back) }

// step opens the review d away, oldest first, as the inspector steps
// between agents. A review not yet recorded is past the newest.
func (r *reviewModal) step(m *Model, d int) {
	i := slices.IndexFunc(m.reviews, func(rec *reviewRecord) bool { return rec.id == r.id })
	if i < 0 {
		i = len(m.reviews)
	}
	i += d
	if i < 0 || i >= len(m.reviews) {
		return
	}
	*m, _ = m.openReview(m.reviews[i])
}

// load switches the review to scope and asks for its changes. A branch's
// base is only known once they come, so its review is found then.
func (r *reviewModal) load(m *Model, scope event.ReviewScope) {
	if r.request == nil {
		r.request = r.block
	}
	base, head, _ := r.trees(*m, scope)
	r.scope, r.base, r.head, r.loading = scope, base, head, true
	r.files, r.err, r.cut, r.file, r.line, r.ranging, r.edit, r.triage = nil, "", false, 0, 0, false, nil, nil
	r.block = r.request
	if scope != event.ScopeRequest {
		r.block = m.lastReviewable(scope != event.ScopeBranch)
	}
	if scope == event.ScopeBranch {
		r.id = uuid.Nil
		m.send(event.LoadDiff{Branch: true, Against: r.against})
		return
	}
	r.id = m.openReviewOf(r.reviewed(), base, head)
	m.send(event.LoadDiff{Base: base, Head: head})
}

// nextScope cycles to the next scope with something to show.
func (r *reviewModal) nextScope(m *Model) {
	i := slices.Index(reviewScopes, r.scope)
	for range len(reviewScopes) - 1 {
		i = (i + 1) % len(reviewScopes)
		if r.scopeOpen(*m, reviewScopes[i]) {
			r.load(m, reviewScopes[i])
			return
		}
	}
}

// trees is what a scope compares, the session ending where since begins,
// and ok is false for a scope with nothing to show.
func (r *reviewModal) trees(m Model, scope event.ReviewScope) (base, head string, ok bool) {
	switch scope {
	case event.ScopeSession:
		first, last := m.firstReviewable(), m.lastReviewable(true)
		if first == nil || last == nil || first == last {
			return "", "", false
		}
		return first.base, m.reviewHead(last), true
	case event.ScopeSince:
		last := m.lastReviewable(true)
		if last == nil || m.cur != nil {
			return "", "", false
		}
		return m.reviewHead(last), "", true
	case event.ScopeBranch:
		return "", "", true
	case event.ScopeRequest:
	}
	if r.request == nil {
		return "", "", false
	}
	return r.request.base, m.reviewHead(r.request), true
}

func (r *reviewModal) scopeOpen(m Model, scope event.ReviewScope) bool {
	_, _, ok := r.trees(m, scope)
	return ok
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

// lastReviewable is the newest request with its files checkpointed, or with
// ended the newest whose end is known too.
func (m Model) lastReviewable(ended bool) *turnBlock {
	for _, b := range slices.Backward(m.blocks) {
		if b.base != "" && (!ended || m.reviewHead(b) != "") {
			return b
		}
	}
	return nil
}

// reviewableBefore is the newest request before b with its files checkpointed.
func (m Model) reviewableBefore(b *turnBlock) *turnBlock {
	i := slices.Index(m.blocks, b)
	for j := i - 1; j >= 0; j-- {
		if m.blocks[j].base != "" {
			return m.blocks[j]
		}
	}
	return nil
}

func (m Model) firstReviewable() *turnBlock {
	for _, b := range m.blocks {
		if b.base != "" {
			return b
		}
	}
	return nil
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

// reviewed is the request a review belongs to, which undoing it drops, and
// none for a branch, which no request owns.
func (r *reviewModal) reviewed() uuid.UUID {
	if r.scope == event.ScopeBranch || r.block == nil {
		return uuid.Nil
	}
	return r.block.id
}

// diffLoaded fills the review it answers, defusing what the files say once, here.
// A branch's answer names where it left its base, which finds its review.
func (m *Model) diffLoaded(v event.DiffLoaded) {
	r := modalAs[*reviewModal](*m)
	if r == nil {
		return
	}
	branch := r.scope == event.ScopeBranch
	if !r.loading || v.Branch != branch || !branch && (v.Base != r.base || v.Head != r.head) {
		return
	}
	r.loading, r.cut, r.err = false, v.Cut, v.Err
	r.files, r.raw, r.code = defused(v.Files), v.Files, map[hunkKey][]string{}
	if r.stepBack && r.scope == event.ScopeRequest && len(r.files) == 0 && r.err == "" {
		// The last request may only have answered a question, so the one before is meant.
		if prev := m.reviewableBefore(r.request); prev != nil {
			r.request = prev
			r.load(m, r.scope)
			return
		}
	}
	r.stepBack = false
	if branch {
		r.base, r.against = v.Base, v.Against
		// A review opened by name keeps its id, though main may have moved since.
		if !r.pinned {
			r.id = m.openReviewOf(uuid.Nil, v.Base, "")
		}
	}
}

// reviewStarted records a review as its reviewer begins, so /review, s and the
// arrows find it before it has commented on anything.
func (m *Model) reviewStarted(v event.ReviewStarted) {
	if m.reviewByID(v.Review) == nil {
		m.reviews = append(m.reviews, &reviewRecord{id: v.Review, reviewed: v.Reviewed, scope: v.Scope,
			against: v.Against, base: v.Base, head: v.Head})
	}
	for _, b := range m.blocks {
		if b.review == v.Review {
			for _, r := range b.rows {
				r.files = v.Files
			}
			b.rev++
		}
	}
}

// reviewCommented folds one comment into its review, made on its first.
func (m *Model) reviewCommented(v event.ReviewCommented) {
	rec := m.reviewByID(v.Review)
	if rec == nil {
		rec = &reviewRecord{id: v.Review, reviewed: v.Reviewed, scope: scopeOf(v), against: v.Against,
			base: v.Base, head: v.Head}
		m.reviews = append(m.reviews, rec)
	}
	defer m.countComments(rec)
	c := defusedComment(v.Comment)
	switch v.Op {
	case event.CommentAdded:
		rec.comments = append(rec.comments, c)
		if !m.replaying {
			if rec.arrived == nil {
				rec.arrived = map[uuid.UUID]time.Time{}
			}
			rec.arrived[c.ID] = time.Now()
		}
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
	if r := modalAs[*reviewModal](*m); r != nil && r.id == v.Review {
		r.close(m)
		m.noteOK(fmt.Sprintf("sent %s to the agent", countOf(v.Comments, "comment")))
	}
}

// startReviewBlock is a review's Turn: one row, which enter opens, and no
// prompt, since nobody asked a request of the model.
func (m *Model) startReviewBlock(v event.TurnStarted) {
	row := &historyRow{id: v.Turn, review: v.Review, running: true}
	if rec := m.reviewByID(v.Review); rec != nil {
		row.comments = len(rec.comments)
	}
	b := &turnBlock{id: v.Turn, prompt: v.Prompt, review: v.Review, rows: []*historyRow{row}}
	m.blocks = append(m.blocks, b)
	m.setCur(b)
	m.followNewest()
}

// countComments keeps a review's history row in step with its comments, as a
// block draws from its own state alone.
func (m *Model) countComments(rec *reviewRecord) {
	for _, b := range m.blocks {
		if b.review != rec.id {
			continue
		}
		for _, r := range b.rows {
			r.comments = len(rec.comments)
		}
		b.rev++
	}
}

// move steps the line in the diff pane, or the file in the list.
func (r *reviewModal) move(m *Model, d int) {
	if !r.diffFocused {
		r.pickFile(r.file + d)
		return
	}
	r.line = min(max(r.line+d, 0), max(0, len(r.rows(*m))-1))
}

// pickFile selects file i, from its first line.
func (r *reviewModal) pickFile(i int) {
	r.file, r.line, r.ranging = min(max(i, 0), max(0, len(r.files)-1)), 0, false
}

// toggleRange starts a range of lines at the cursor, or drops one.
func (r *reviewModal) toggleRange(m Model) {
	rows := r.rows(m)
	if r.ranging || r.line >= len(rows) || rows[r.line].line < 0 {
		r.ranging = false
		return
	}
	r.ranging, r.anchor = true, r.line
}

// startComment opens the editor: a reply on a comment, else a new comment
// on the range or the line under the cursor.
func (r *reviewModal) startComment(m *Model) {
	rows := r.rows(*m)
	if !r.diffFocused || r.line >= len(rows) {
		return
	}
	if c := rows[r.line].comment; c != nil {
		root := c.ID
		if c.ReplyTo != uuid.Nil {
			root = c.ReplyTo
		}
		r.edit = newCommentEdit(event.CommentAdded, root, "")
		return
	}
	from, to := r.line, r.line
	if r.ranging {
		from, to = min(r.anchor, r.line), max(r.anchor, r.line)
	}
	if rows[from].line < 0 || rows[from].hunk != rows[to].hunk {
		m.noteErr("a comment covers lines of one hunk")
		return
	}
	r.edit = newCommentEdit(event.CommentAdded, uuid.Nil, "")
	r.edit.from, r.edit.to = from, to
}

// startEdit opens the editor on the comment under the cursor, its words in it.
func (r *reviewModal) startEdit(m Model) {
	rows := r.rows(m)
	if r.line >= len(rows) || rows[r.line].comment == nil {
		return
	}
	c := rows[r.line].comment
	r.edit = newCommentEdit(event.CommentEdited, c.ID, c.Body)
}

// deleteComment deletes the comment under the cursor on a second x, since a
// reviewer's comment, once gone, cannot be written again.
func (r *reviewModal) deleteComment(m Model) {
	rows := r.rows(m)
	if r.line >= len(rows) || rows[r.line].comment == nil {
		return
	}
	c := rows[r.line].comment
	if r.deleting != c.ID {
		r.deleting = c.ID
		return
	}
	r.deleting = uuid.Nil
	r.line = max(0, r.line-1)
	r.sendComment(m, event.CommentDeleted, event.ReviewComment{ID: c.ID})
}

// editKey writes into the editor: enter saves, esc drops the draft.
func (r *reviewModal) editKey(m *Model, msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keymap.review.drop):
		r.edit = nil
		return nil
	case key.Matches(msg, keymap.review.save):
		e := r.edit
		r.edit, r.ranging = nil, false
		body := strings.TrimSpace(e.input.Value())
		if body == "" {
			return nil
		}
		r.saveComment(*m, e, body)
		// An edit made in triage counts as kept, and moves on.
		if r.triage != nil && e.op == event.CommentEdited {
			r.triage.kept++
			r.nextTriage(m)
		}
		return nil
	}
	var cmd tea.Cmd
	r.edit.input, cmd = r.edit.input.Update(msg)
	return cmd
}

// saveComment sends what the editor holds as the comment it was opened for.
func (r *reviewModal) saveComment(m Model, e *commentEdit, body string) {
	if e.op == event.CommentEdited {
		c := r.comment(m, e.target)
		if c == nil {
			return
		}
		// A reviewer's comment the human rewrites is theirs, its first words kept.
		edited := *c
		if edited.Author != "" {
			edited.Original, edited.Author = edited.Body, ""
		}
		edited.Body = body
		r.sendComment(m, event.CommentEdited, edited)
		return
	}
	c := event.ReviewComment{ID: uuid.Must(uuid.NewV7()), Body: body, ReplyTo: e.target}
	if parent := r.comment(m, e.target); parent != nil {
		c.Path, c.Side, c.Start, c.End = parent.Path, parent.Side, parent.Start, parent.End
		r.sendComment(m, event.CommentAdded, c)
		return
	}
	r.anchorAt(m, &c, e.from, e.to)
	r.sendComment(m, event.CommentAdded, c)
}

// anchor places c on rows from to to: the new side's numbers, unless every
// line was removed, with the lines quoted as the diff shows them.
func (r *reviewModal) anchorAt(m Model, c *event.ReviewComment, from, to int) {
	f := r.selected()
	rows := r.rows(m)
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

func (r *reviewModal) sendComment(m Model, op event.CommentOp, c event.ReviewComment) {
	m.send(event.CommentReview{Review: r.id, Reviewed: r.reviewed(), Base: r.base, Head: r.head,
		Scope: r.scope, Op: op, Comment: c})
}

// submit sends the review's comments to the agent as its next prompt.
func (r *reviewModal) submit(m *Model) {
	rec := m.reviewByID(r.id)
	if rec == nil || len(rec.comments) == 0 {
		m.noteErr("nothing to send: c comments on the line under the cursor")
		return
	}
	n := 0
	if r.block != nil {
		n = r.block.n
	}
	m.send(event.SubmitReview{Review: rec.id, Scope: r.scope, Request: n,
		Against: r.against, Comments: slices.Clone(rec.comments)})
}

// startReviewer asks for a reviewer on what the modal shows, the scope chosen
// and its diff on screen, so the reviewer reads exactly what the human does.
func (r *reviewModal) startReviewer(m *Model) {
	switch {
	case !m.run.Subagents:
		m.noteErr("a reviewer needs subagents on: set subagents: true in your config")
		return
	case r.loading || r.err != "" || len(r.raw) == 0:
		m.noteErr("nothing here for a reviewer to read")
		return
	case m.cur != nil:
		m.noteErr("a request is running: start the reviewer once it ends")
		return
	}
	n := 0
	if r.block != nil {
		n = r.block.n
	}
	m.send(event.ReviewChanges{Review: r.id, Reviewed: r.reviewed(), Scope: r.scope,
		Base: r.base, Head: r.head, Against: r.against, Request: n, Asked: r.asked(*m), Files: r.raw})
}

// asked is what the changes were made for: one request's prompt, every
// request's for a wider scope, and nothing for the human's own edits.
func (r *reviewModal) asked(m Model) string {
	switch r.scope {
	case event.ScopeSince:
		return ""
	case event.ScopeRequest:
		if r.block != nil {
			return r.block.prompt
		}
		return ""
	case event.ScopeSession, event.ScopeBranch:
	}
	var b strings.Builder
	for _, blk := range m.blocks {
		if blk.n > 0 && blk.review == uuid.Nil {
			fmt.Fprintf(&b, "%d. %s\n", blk.n, blk.prompt)
		}
	}
	return strings.TrimSpace(b.String())
}

// startTriage goes through the reviewer's comments on lines one at a time, in
// the order of the files and their lines, the way a human curates a review.
func (r *reviewModal) startTriage(m *Model) {
	t := &triageState{}
	for _, c := range r.lineComments(*m) {
		if c.Author != "" {
			t.queue = append(t.queue, c.ID)
		}
	}
	if len(t.queue) == 0 {
		m.noteErr("no reviewer comments to go through")
		return
	}
	r.triage = t
	r.focusComment(*m, t.queue[0])
}

// triageKey answers the comment under triage: y keeps it, n drops it, e
// rewrites it, and esc stops. It is false for any other key.
func (r *reviewModal) triageKey(m *Model, msg tea.KeyPressMsg) bool {
	t := r.triage
	k := keymap.review
	switch {
	case key.Matches(msg, k.keep):
		t.kept++
		r.nextTriage(m)
	case key.Matches(msg, k.discard):
		t.dropped++
		r.sendComment(*m, event.CommentDeleted, event.ReviewComment{ID: t.queue[t.at]})
		r.nextTriage(m)
	case key.Matches(msg, k.edit):
		r.focusComment(*m, t.queue[t.at])
		r.startEdit(*m)
	case key.Matches(msg, k.close):
		r.triage = nil
	default:
		return false
	}
	return true
}

// nextTriage moves to the next comment still there, or ends the walk saying how it went.
func (r *reviewModal) nextTriage(m *Model) {
	t := r.triage
	for t.at++; t.at < len(t.queue); t.at++ {
		if r.comment(*m, t.queue[t.at]) != nil {
			r.focusComment(*m, t.queue[t.at])
			return
		}
	}
	r.triage = nil
	m.noteOK(fmt.Sprintf("went through the reviewer's comments: kept %d, dropped %d", t.kept, t.dropped))
}

// focusComment puts the diff cursor on a comment, in its file.
func (r *reviewModal) focusComment(m Model, id uuid.UUID) {
	c := r.comment(m, id)
	if c == nil {
		return
	}
	if i := slices.IndexFunc(r.files, func(f event.FileDiff) bool { return f.Path == c.Path }); i >= 0 {
		r.file = i
	}
	r.diffFocused, r.ranging = true, false
	for i, row := range r.rows(m) {
		if row.comment != nil && row.comment.ID == id && row.author {
			r.line = i
			return
		}
	}
}

// jumpComment moves to the next comment in direction d, across files: from a
// comment to its neighbour, else to the nearest past the cursor.
func (r *reviewModal) jumpComment(m *Model, d int) {
	comments := r.lineComments(*m)
	if len(comments) == 0 {
		m.noteErr("no comments on lines yet: c adds one")
		return
	}
	order := func(path string) int {
		return slices.IndexFunc(r.files, func(f event.FileDiff) bool { return f.Path == path })
	}
	file, line, on := r.file, 0, -1
	if rows := r.rows(*m); r.line < len(rows) {
		row := rows[r.line]
		switch {
		case row.comment != nil:
			on = slices.IndexFunc(comments, func(c *event.ReviewComment) bool {
				return c.ID == row.comment.ID || c.ID == row.comment.ReplyTo
			})
		case row.line >= 0:
			l := r.files[r.file].Hunks[row.hunk].Lines[row.line]
			line = max(l.New, l.Old)
		}
	}
	next := on + d
	if on < 0 {
		// Not on a comment: the first one past the cursor that way.
		next = -1
		for i, c := range comments {
			at := cmp.Or(cmp.Compare(order(c.Path), file), cmp.Compare(c.End, line))
			if d > 0 && at > 0 && next < 0 {
				next = i
			}
			if d < 0 && at < 0 {
				next = i
			}
		}
	}
	if next < 0 || next >= len(comments) {
		m.noteOK("no more comments that way")
		return
	}
	r.focusComment(*m, comments[next].ID)
}

// toggleViewed marks the file under the cursor viewed, and moves on to the next
// one not yet viewed, or unmarks it. Kept for the session, never stored.
func (r *reviewModal) toggleViewed(m *Model) {
	f := r.selected()
	if f == nil {
		return
	}
	if m.viewed == nil {
		m.viewed = map[uuid.UUID]map[string]bool{}
	}
	seen := m.viewed[r.id]
	if seen == nil {
		seen = map[string]bool{}
		m.viewed[r.id] = seen
	}
	if seen[f.Path] {
		delete(seen, f.Path)
		return
	}
	seen[f.Path] = true
	for i := range len(r.files) {
		next := (r.file + 1 + i) % len(r.files)
		if !seen[r.files[next].Path] {
			r.pickFile(next)
			return
		}
	}
	m.noteOK(fmt.Sprintf("every file viewed, %d of %d", len(seen), len(r.files)))
}

// freshness is how far into its fade a comment is, 0 just in and 1 or more done.
func (r *reviewModal) freshness(m Model, id uuid.UUID) float64 {
	rec := m.reviewByID(r.id)
	if rec == nil {
		return 1
	}
	at, ok := rec.arrived[id]
	if !ok {
		return 1
	}
	return float64(time.Since(at)) / float64(fadeFor)
}

// fading is whether a comment of the open review still stands out.
func (m Model) fading() bool {
	r := modalAs[*reviewModal](m)
	if r == nil {
		return false
	}
	rec := m.reviewByID(r.id)
	if rec == nil {
		return false
	}
	for _, at := range rec.arrived {
		if time.Since(at) < fadeFor {
			return true
		}
	}
	return false
}

// fadeTick keeps one tick going while a comment fades, and none otherwise.
func (m *Model) fadeTick() tea.Cmd {
	if m.fadeTicking || !m.fading() {
		return nil
	}
	m.fadeTicking = true
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return fadeMsg{} })
}

// reviewer is the reviewer working on the open review, nil when none is.
func (r *reviewModal) reviewer(m Model) *agentState {
	for _, a := range m.agentOrder {
		if rv := reviewerOf(a); rv != nil && rv.review == r.id && !a.ended {
			return a
		}
	}
	return nil
}

// sent is whether the review shown was sent, which leaves it to read.
func (r *reviewModal) sent(m Model) bool {
	rec := m.reviewByID(r.id)
	return rec != nil && rec.submitted
}

func (m Model) reviewByID(id uuid.UUID) *reviewRecord {
	for _, r := range m.reviews {
		if r.id == id {
			return r
		}
	}
	return nil
}

func (r *reviewModal) comment(m Model, id uuid.UUID) *event.ReviewComment {
	rec := m.reviewByID(r.id)
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

// blockByID finds a block without marking it to redraw, as block does.
func (m Model) blockByID(id uuid.UUID) *turnBlock {
	for _, b := range m.blocks {
		if id != uuid.Nil && b.id == id {
			return b
		}
	}
	return nil
}

// selected is the file under the cursor, nil while there is none.
func (r *reviewModal) selected() *event.FileDiff {
	if r.file >= len(r.files) {
		return nil
	}
	return &r.files[r.file]
}

// rows is the selected file as the diff pane lists it: each hunk's header,
// its lines, and under the line a comment ends on, the comment and its replies.
func (r *reviewModal) rows(m Model) []diffRow {
	f := r.selected()
	if f == nil {
		return nil
	}
	comments := r.fileComments(m, f.Path)
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

// fileComments is the open review's comments on path.
func (r *reviewModal) fileComments(m Model, path string) []event.ReviewComment {
	rec := m.reviewByID(r.id)
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

// lineComments is the open review's comments on lines, in file then line
// order: what > and < step through. Replies ride with their comment.
func (r *reviewModal) lineComments(m Model) []*event.ReviewComment {
	rec := m.reviewByID(r.id)
	if rec == nil {
		return nil
	}
	var out []*event.ReviewComment
	for i := range rec.comments {
		if c := &rec.comments[i]; c.Path != "" && c.ReplyTo == uuid.Nil {
			out = append(out, c)
		}
	}
	order := func(path string) int {
		return slices.IndexFunc(r.files, func(f event.FileDiff) bool { return f.Path == path })
	}
	slices.SortStableFunc(out, func(a, b *event.ReviewComment) int {
		return cmp.Or(cmp.Compare(order(a.Path), order(b.Path)), cmp.Compare(a.End, b.End))
	})
	return out
}

// nextHunk is the row of the next hunk's header in direction d, or the line
// unmoved when there is none that way.
func (r *reviewModal) nextHunk(m Model, d int) int {
	rows := r.rows(m)
	for i := r.line + d; i >= 0 && i < len(rows); i += d {
		if rows[i].line < 0 && rows[i].comment == nil {
			return i
		}
	}
	return r.line
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

// scopeOf is a comment's scope. One stored before comments said is a branch's
// when no request owns it, since only a branch's never has one, else a request's.
func scopeOf(v event.ReviewCommented) event.ReviewScope {
	switch {
	case v.Scope != "":
		return v.Scope
	case v.Reviewed == uuid.Nil:
		return event.ScopeBranch
	}
	return event.ScopeRequest
}

// scopeNoun is what a scope is called in a sentence.
func scopeNoun(s event.ReviewScope) string {
	switch s {
	case event.ScopeSession:
		return "session of more than one request"
	case event.ScopeSince:
		return "edits since the last request"
	case event.ScopeBranch:
		return "branch"
	case event.ScopeRequest:
	}
	return "request"
}

// endsOn is whether line is the last a comment covers, on the side it numbers.
func endsOn(c event.ReviewComment, l event.DiffLine) bool {
	if c.Side == "old" {
		return l.Op == event.LineRemoved && l.Old == c.End
	}
	return l.New != 0 && l.New == c.End
}

// defused is files with every path and line made safe to print. A copy, since
// an event is shared, and a CRLF line loses its \r rather than showing it.
func defused(files []event.FileDiff) []event.FileDiff {
	out := make([]event.FileDiff, len(files))
	for i, f := range files {
		f.Path = defuse.Text(f.Path)
		f.Hunks = slices.Clone(f.Hunks)
		for j, h := range f.Hunks {
			h.Header = defuse.Text(h.Header)
			h.Lines = slices.Clone(h.Lines)
			for k, l := range h.Lines {
				h.Lines[k].Text = defuse.Text(strings.TrimSuffix(l.Text, "\r"))
			}
			f.Hunks[j] = h
		}
		out[i] = f
	}
	return out
}

// defusedComment is c safe to print, since a reviewer's words are a model's.
func defusedComment(c event.ReviewComment) event.ReviewComment {
	c.Author = defuse.Text(c.Author)
	c.Path = defuse.Text(c.Path)
	c.Quote = defuse.Text(c.Quote)
	c.Body = defuse.Text(c.Body)
	c.Original = defuse.Text(c.Original)
	return c
}
