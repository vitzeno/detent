package ui

import (
	"github.com/google/uuid"
	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/viewspec"
)

// What history is a list of: one turnBlock per prompt, holding the
// calls made toward it. Folded from the event stream, nothing else.

type turnBlock struct {
	id     uuid.UUID
	n      int
	prompt string
	rows   []*callRow

	ended   bool
	end     event.EndReason
	summary string
	used    event.Usage

	// undoable is set once a checkpoint lands, so /undo offers
	// only what it can actually restore.
	undoable bool
	err      string

	// cache is this block's last drawing, behind a pointer so the copy
	// of Model that View works on can still fill it.
	cache *blockCache
}

// blockKey is everything a block's drawing depends on. Comparable, so
// a hit is one equality check. Miss it and the pane renders stale.
type blockKey struct {
	rev, width int
	focused    *callRow // nil unless the cursor is in this block
	spinner    string   // only the live block ever draws one
}

type blockCache struct {
	key      blockKey
	lines    []string
	cursorAt int
}

// callRow is one tool call, or the model's own words. Exactly one of
// tool and prose is set.
type callRow struct {
	id uuid.UUID
	// command is what the human reads: the tool's arguments, rendered.
	command string
	prose   string

	risk    event.Risk
	running bool
	live    []string
	dropped int

	result *event.Result
	post   *verdict

	expanded    bool
	tableCursor int

	// view is this row's bound spec, resolved once. viewTried marks
	// the attempt so a spec that does not fit is not retried per frame.
	view       *viewspec.Bound
	viewTried  bool
	viewSource string
}

// verdict is the post-execution read, when one arrived.
type verdict struct {
	status     string
	renderKind string
	attention  float64
	fromJudge  bool
}

// text is what the row draws in the output pane.
func (r *callRow) text() string {
	if r.prose != "" {
		return r.prose
	}
	if r.result == nil {
		return ""
	}
	return outputOf(r.result)
}

// drawable reports whether there is settled text to draw. False while
// a call streams, since live output has its own path.
func (r *callRow) drawable() bool {
	return r != nil && !r.running && (r.prose != "" || r.result != nil)
}

// kind is the judged render kind, or "" while pending.
func (r *callRow) kind() string {
	if r == nil || r.running || r.post == nil {
		return ""
	}
	return r.post.renderKind
}

func outputOf(r *event.Result) string {
	switch {
	case r.Err != "":
		return r.Err
	case r.Stdout != "" && r.Stderr != "":
		return r.Stdout + "\n" + r.Stderr
	case r.Stderr != "":
		return r.Stderr
	}
	return r.Stdout
}

// ok reports whether the call succeeded, for the row's status glyph.
func (r *callRow) ok() bool {
	return r.result != nil && r.result.Err == "" && r.result.ExitCode == 0
}
