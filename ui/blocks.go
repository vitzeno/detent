package ui

import (
	"github.com/vitzeno/detent/ui/editor"
	"github.com/vitzeno/detent/ui/tree"
	"github.com/vitzeno/detent/viewspec"
)

// What the history pane is a list of: one goalBlock per goal (or
// slash-command invocation), each holding the steps taken toward it.
// Model owns the blocks; these types own nothing but their own data.

type goalBlock struct {
	goal    string
	res     *GoalResult // nil for a tool block
	steps   []*stepRow
	ended   bool
	end     EndReason
	summary string
	// judge is Jev's read on whether this goal was actually met. Kept
	// as the score rather than a rendered line, so the banner styles
	// it at render time and a theme change follows.
	judge    goalJudgement
	fatalErr error

	// tool names a slash-command invocation. Empty for a real goal.
	tool string
}

type stepRow struct {
	command string

	// prose is the model's closing words rather than a command.
	// Judged and drawn like output: the question is the same one, does
	// this read better as something other than plain text.
	prose string

	// editPath is set at approve() time; editor fills in once the
	// command finishes and the file is read from disk. Shared by both a
	// command row and a tree-opened file row.
	editPath string
	editor   *editor.Model

	// toolKind is non-empty for a tool row instead of an executed
	// command. Exactly one of cmd/tool is meaningful.
	toolKind string
	cmd      cmdState
	tool     toolState
}

// text is what the row draws in the output pane: a command's output,
// or the model's own words.
func (r *stepRow) text() string {
	if r.prose != "" {
		return r.prose
	}
	if r.cmd.ec == nil {
		return ""
	}
	return commandOutput(r.cmd.ec)
}

// verdict is the judgement on this row, wherever it was attached: to
// the executed command, or to the row itself when nothing ran.
func (r *stepRow) verdict() *PostJudgment {
	if r.cmd.ec != nil {
		return r.cmd.ec.Post
	}
	return r.cmd.post
}

// drawable reports whether there is settled text to draw. False while
// a command streams, since live output has its own path.
func (r *stepRow) drawable() bool {
	return r != nil && !r.cmd.running && (r.prose != "" || r.cmd.ec != nil)
}

// cmdState holds a stepRow's fields for an executed command; zero-valued
// on a tool row (toolKind != "").
type cmdState struct {
	rationale string
	pre       PreJudgment
	live      []string
	dropped   int
	ec        *ExecutedCommand // nil while running; judgeMsg attaches Post
	// post is where judgeMsg puts its verdict when there is no ec to
	// hang it on, which is a prose row. Read through verdict.
	post     *PostJudgment
	running  bool
	expanded bool
	step     StepHandle // correlates with Driver.RecordStep/JudgeResult

	tableCursor int // the view's selected row

	// view is the row's bound viewspec, resolved once; viewTried marks
	// the attempt so a spec that doesn't fit is not retried per frame.
	view      *viewspec.Bound
	viewTried bool
	// generated marks that a view was already asked for, so a redraw
	// cannot fire a second model call for the same row.
	generated bool
	// viewSource is where the drawn view's framing came from; empty
	// means the built-in fallback for its judged kind.
	viewSource ViewSource
	// viewDeclined records that a model was asked and nothing it
	// returned survived, so the pane can say so rather than look
	// identical to a view never attempted.
	viewDeclined bool
}

// toolState holds a stepRow's fields for a slash-command row. Only
// /tree is one now: the pages about the session are panels, which are
// not rows at all.
type toolState struct {
	tree *tree.Model
}

// goalJudgement is the second opinion on a finished goal. scored is
// false when no Judge was wired, or it declined to answer — which is
// different from a low score and must not read like one.
type goalJudgement struct {
	scored bool
	score  float64
}

// rowKind returns the judged kind, or "" while pending.
func rowKind(r *stepRow) RenderKind {
	if r == nil || r.cmd.running {
		return ""
	}
	p := r.verdict()
	if p == nil {
		return ""
	}
	return p.RenderKind
}
