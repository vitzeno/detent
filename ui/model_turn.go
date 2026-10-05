package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/search"
	"github.com/vitzeno/detent/viewspec"
)

// What history is a list of: one turnBlock per prompt, holding the
// calls made toward it. Folded from the event stream, nothing else.

type turnBlock struct {
	id     uuid.UUID
	n      int
	prompt string
	rows   []*historyRow

	ended   bool
	end     event.EndReason
	summary string
	used    event.Usage

	// userCommands marks a block holding commands the human ran between Turns.
	// Not a Turn: no prompt, no checkpoint, nothing to undo.
	userCommands bool
	// seam is set on a block that marks a resume: one line, no rows.
	seam *event.SessionResumed
	// undoable is set once a checkpoint lands, so /undo offers
	// only what it can actually restore.
	undoable bool
	// files and container say what that checkpoint covers.
	files, container bool
	err              string

	// rev moves whenever anything this block draws does, and its cache
	// is keyed on it, so one live line redraws one block.
	rev int
	// cache is this block's last drawing, behind a pointer so the copy
	// of Model that View works on can still fill it.
	cache *blockCache
}

// historyRow is one tool call, or the model's own words. Exactly one of
// command and prose is set.
type historyRow struct {
	id uuid.UUID
	// command is what the human reads: the tool's arguments, rendered.
	command string
	prose   string
	// tool is the tool's name, empty for a command the human ran, and
	// headline its arguments as history leads with them.
	tool, headline string
	// wrote is what write_file was given, kept until its result says whether
	// the file was new, when created becomes that content as a diff.
	wrote   *written
	created string
	// renders is the shape the tool declared, which beats a judged
	// guess because the tool knows and the judge is estimating.
	renders event.RenderKind
	// executor is empty for a shell command. Anything else ran outside
	// the sandbox, so no checkpoint can take it back.
	executor string
	// human is a command the person typed, not one the model proposed, and
	// child one a subagent ran, which the judge never grades either.
	human bool
	child bool
	// signin is set on a row that is a server asking to be signed in to.
	signin *signInState
	// agent is set on a spawn_agent row, which draws the subagent it started.
	agent *agentState

	risk    event.Risk
	running bool
	live    []string
	dropped int

	result *event.Result
	took   time.Duration
	post   *verdict

	expanded    bool
	tableCursor int

	// found is the row's text made ready for the finder, and foundOf the
	// result it was made from, so a new result makes it again.
	found   *search.Text
	foundOf *event.Result

	// view is this row's bound spec, resolved once. viewTried marks
	// the attempt so a spec that does not fit is not retried per frame.
	view       *viewspec.Bound
	viewTried  bool
	viewSource string
}

// agentState is one subagent, folded from its facts. Its rows are its own and
// never in a Turn block, so main history shows it as its spawn's one row.
type agentState struct {
	id         uuid.UUID
	name, task string
	// spawn is the parent's spawn_agent row, which draws this agent.
	spawn *historyRow
	rows  []*historyRow
	steps int
	// calls counts what it asked for, and last is the newest one's headline.
	calls int
	last  string
	// used is its own spend, from its Steps, and ctx its last prompt's size,
	// which is how full its context is.
	used event.Usage
	ctx  int
	// waitingSince is when its question went up, zero while none waits.
	waitingSince time.Time
	ended        bool
	reason       event.AgentReason
}

// verdict is the post-execution read, when one arrived.
type verdict struct {
	status     event.Status
	renderKind event.RenderKind
	attention  float64
	fromJudge  bool
}

// written is a write_file call's path and content, defused.
type written struct{ path, content string }

// spawnTool is internal/tool.SpawnAgentName, which ui cannot import.
const spawnTool = "spawn_agent"

// newFileReport is how write_file says it created a file, rather than
// showing a diff the model would pay to read (internal/tool/write_file.go).
const newFileReport = "created "

// text is what the row draws in the output pane.
func (r *historyRow) text() string {
	if r.prose != "" {
		return r.prose
	}
	if r.result == nil {
		return ""
	}
	if r.created != "" {
		return r.created
	}
	return outputOf(r.result)
}

// drawable reports whether there is settled text to draw. False while
// a call streams, since live output has its own path.
func (r *historyRow) drawable() bool {
	return r != nil && !r.running && (r.prose != "" || r.result != nil)
}

// kind is the judged render kind, or "" while pending.
func (r *historyRow) kind() event.RenderKind {
	if r == nil || r.running || r.post == nil {
		return ""
	}
	return r.post.renderKind
}

// ok reports whether the call succeeded, for the row's status glyph.
func (r *historyRow) ok() bool {
	return r.result != nil && r.result.Err == "" && r.result.ExitCode == 0
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

// addedDiff is a new file as a unified diff that adds every line, so the
// human sees it as they would an edit.
func addedDiff(w *written) string {
	lines := strings.Split(strings.TrimSuffix(w.content, "\n"), "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n@@ -0,0 +1,%d @@\n", w.path, w.path, len(lines))
	for _, l := range lines {
		b.WriteString("+" + l + "\n")
	}
	return b.String()
}
