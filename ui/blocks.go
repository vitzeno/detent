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

// cmdState holds a stepRow's fields for an executed command; zero-valued
// on a tool row (toolKind != "").
type cmdState struct {
	rationale string
	pre       PreJudgment
	live      []string
	dropped   int
	ec        *ExecutedCommand // nil while running; judgeMsg attaches Post
	running   bool
	expanded  bool
	step      StepHandle // correlates with Driver.RecordStep/JudgeResult

	tableCursor int // the view's selected row

	// view is the row's bound viewspec, resolved once; viewTried marks
	// the attempt so a spec that doesn't fit is not retried per frame.
	view      *viewspec.Bound
	viewTried bool
	// generated marks that a view was already asked for, so a redraw
	// cannot fire a second model call for the same row.
	generated bool
}

// toolState holds a stepRow's fields for a slash-command row (/tree,
// /usage, /help, or a tree-opened file); zero-valued on a command row.
type toolState struct {
	tree        *tree.Model // toolKind == "tree"
	usageCursor int         // toolKind == "usage"
	usageExpand int
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
	if r == nil || r.cmd.running || r.cmd.ec == nil || r.cmd.ec.Post == nil {
		return ""
	}
	return r.cmd.ec.Post.RenderKind
}
