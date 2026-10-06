package ui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Key bindings. Every place that takes keys has a keymap here, and its handler
// matches against it and its hints are drawn from it, so they cannot drift apart.

// farAway is a scroll long enough to reach either end, since every scroll clamps.
const farAway = 1 << 30

// moveKeys move through a list or scroll a pane, the same everywhere.
type moveKeys struct {
	up, down, pageUp, pageDown, top, bottom key.Binding
}

// appKeys work wherever no question or modal holds the keys.
type appKeys struct {
	quit, find, esc, tab, entry key.Binding
}

// inputKeys are the prompt's, beside what it types.
type inputKeys struct {
	run, pageUp, pageDown key.Binding
	pick                  moveKeys
	complete              key.Binding
}

// historyKeys are the history pane's.
type historyKeys struct {
	move                   moveKeys
	open, expand, agents   key.Binding
	stop, signIn, copyLink key.Binding
}

// outputKeys are the output pane's.
type outputKeys struct {
	move   moveKeys
	expand key.Binding
}

// askKeys answer a question: an approval, the step bound, undo or delete.
type askKeys struct {
	yes, no, enter, esc key.Binding
	read                moveKeys
}

// finderKeys are the finder's, whose letters are its query.
type finderKeys struct {
	close, jump, kind, erase, eraseWord key.Binding
	move                                moveKeys
}

// inspectorKeys are the inspector's.
type inspectorKeys struct {
	close, pane, prevAgent, nextAgent, stop, yes, no key.Binding
	move                                             moveKeys
}

// resumeKeys are the resume picker's.
type resumeKeys struct {
	close, resume, pane key.Binding
	move                moveKeys
}

// reviewKeys are the review's, then its editor's and its triage's.
type reviewKeys struct {
	close, pane, diff, file, prevFile, hunk, prevHunk, rng key.Binding
	comment, edit, remove, send, scope, reviewer           key.Binding
	prevReview, nextReview, prevComment, nextComment       key.Binding
	triage, viewed, split                                  key.Binding
	move                                                   moveKeys
	save, drop, keep, discard                              key.Binding
}

// keymap is every binding detent has.
var keymap = struct {
	app       appKeys
	input     inputKeys
	history   historyKeys
	output    outputKeys
	ask       askKeys
	finder    finderKeys
	inspector inspectorKeys
	resume    resumeKeys
	review    reviewKeys
}{
	app: appKeys{
		quit:  bind("ctrl+c", "quit", "ctrl+c"),
		find:  bind("ctrl+f", "find anything in history and jump to it", "ctrl+f"),
		esc:   bind("esc", "back out of the innermost thing, or twice stops what runs", "esc"),
		tab:   bind("tab", "move between input, history and output", "tab"),
		entry: bind("shift+tab", "switch the bar between a request and a command", "shift+tab"),
	},
	input: inputKeys{
		run:      bind("enter", "run what is typed, or steer a running request", "enter"),
		pageUp:   bind("pgup", "scroll the output", "pgup"),
		pageDown: bind("pgdn", "scroll the output", "pgdown"),
		pick: moveKeys{
			up:   bind("↑", "pick in the slash menu", "up"),
			down: bind("↓", "pick in the slash menu", "down"),
		},
		complete: bind("tab", "complete the slash menu's pick", "tab"),
	},
	history: historyKeys{
		move:     lettered("move the cursor", "scroll the output", "the first row", "the newest row, followed again"),
		open:     bind("enter", "open a review or an agent, or expand a tool call", "enter"),
		expand:   bind("space", "expand a tool call's output inline", "space", "v"),
		agents:   bind("a", "look into the agents", "a"),
		stop:     bind("x", "stop the agent under the cursor, asked twice", "x"),
		signIn:   bind("enter", "open a sign-in link", "enter", "o"),
		copyLink: bind("c", "copy a sign-in link", "c"),
	},
	output: outputKeys{
		move:   lettered("scroll", "scroll a page", "the top", "the end"),
		expand: bind("enter", "seed the prompt from a view's selection, or expand", "enter", "v", "space"),
	},
	ask: askKeys{
		yes:   bind("y", "yes", "y", "Y"),
		no:    bind("n", "no", "n", "N"),
		enter: bind("enter", "yes to an approval or the step bound, the safe answer to undo", "enter"),
		esc:   bind("esc", "cancel", "esc"),
		read:  lettered("read", "read a page", "the top", "the end, read whole"),
	},
	finder: finderKeys{
		close:     bind("esc", "close", "esc"),
		jump:      bind("enter", "jump to it", "enter"),
		kind:      bind("ctrl+f", "look for another kind of thing", "ctrl+f"),
		erase:     bind("backspace", "erase a character", "backspace"),
		eraseWord: bind("ctrl+w", "erase a word", "ctrl+w"),
		move: moveKeys{
			up:       bind("↑", "move", "up", "ctrl+p"),
			down:     bind("↓", "move", "down", "ctrl+n"),
			pageUp:   bind("pgup", "scroll the preview", "pgup", "ctrl+u"),
			pageDown: bind("pgdn", "scroll the preview", "pgdown", "ctrl+d"),
			top:      bind("home", "the first match", "home"),
			bottom:   bind("end", "the last match", "end"),
		},
	},
	inspector: inspectorKeys{
		close:     bind("esc", "back", "esc"),
		pane:      bind("tab", "switch pane", "tab"),
		prevAgent: bind("←", "the agent started before", "left"),
		nextAgent: bind("→", "the agent started after", "right"),
		stop:      bind("x", "stop the agent, asked twice", "x"),
		yes:       bind("y", "run its tool call", "y", "Y", "enter"),
		no:        bind("n", "decline its tool call", "n", "N"),
		move:      lettered("move, or scroll its output", "scroll a page", "the first row or line", "the last"),
	},
	resume: resumeKeys{
		close:  bind("esc", "back", "esc"),
		resume: bind("enter", "resume the session", "enter"),
		pane:   bind("tab", "switch pane", "tab"),
		move:   lettered("move, or scroll the preview", "scroll a page", "the first session or line", "the last"),
	},
	review: reviewKeys{
		close:       bind("esc", "close, a layer at a time", "esc"),
		pane:        bind("tab", "switch pane", "tab"),
		diff:        bind("enter", "into the diff", "enter"),
		file:        bind("n", "next file", "n"),
		prevFile:    bind("p", "previous file", "p"),
		hunk:        bind("]", "next hunk", "]"),
		prevHunk:    bind("[", "previous hunk", "["),
		rng:         bind("v", "start or drop a range of lines", "v"),
		comment:     bind("c", "comment on the line or range, or reply", "c"),
		edit:        bind("e", "rewrite the comment", "e"),
		remove:      bind("x", "delete the comment, asked twice", "x"),
		send:        bind("ctrl+s", "send the review as the next prompt", "ctrl+s"),
		scope:       bind("s", "review another scope", "s"),
		reviewer:    bind("r", "ask a reviewer", "r"),
		prevReview:  bind("←", "the review before", "left"),
		nextReview:  bind("→", "the review after", "right"),
		triage:      bind("t", "walk the reviewer's comments", "t"),
		prevComment: bind("<", "the comment before, across files", "<"),
		nextComment: bind(">", "the comment after, across files", ">"),
		viewed:      bind("space", "mark the file viewed", "space"),
		split:       bind("w", "split the diff side by side", "w"),
		move: moveKeys{
			up:       bind("↑", "move", "up", "k"),
			down:     bind("↓", "move", "down", "j"),
			pageUp:   bind("pgup", "move a page", "pgup"),
			pageDown: bind("pgdn", "move a page", "pgdown"),
			top:      bind("g", "the first line or file", "home", "g"),
			bottom:   bind("G", "the last line or file", "end", "G"),
		},
		save:    bind("enter", "save the comment", "enter"),
		drop:    bind("esc", "drop the draft", "esc"),
		keep:    bind("y", "keep the comment", "y"),
		discard: bind("n", "drop the comment", "n"),
	},
}

// keyGroup is one place's keys, as /help lists them.
type keyGroup struct {
	place string
	keys  []key.Binding
}

// keyGroups is every binding in keymap, by where it works.
func keyGroups() []keyGroup {
	k := keymap
	return []keyGroup{
		{"anywhere", []key.Binding{k.app.quit, k.app.find, k.app.esc, k.app.tab, k.app.entry}},
		{"the prompt", append([]key.Binding{k.input.run, k.input.pageUp, k.input.pageDown, k.input.complete},
			k.input.pick.bindings()...)},
		{"history", append(k.history.move.bindings(), k.history.open, k.history.expand, k.history.agents,
			k.history.stop, k.history.signIn, k.history.copyLink)},
		{"output", append(k.output.move.bindings(), k.output.expand)},
		{"a question", append([]key.Binding{k.ask.yes, k.ask.no, k.ask.enter, k.ask.esc}, k.ask.read.bindings()...)},
		{"the finder", append([]key.Binding{k.finder.close, k.finder.jump, k.finder.kind, k.finder.erase,
			k.finder.eraseWord}, k.finder.move.bindings()...)},
		{"an agent", append([]key.Binding{k.inspector.close, k.inspector.pane, k.inspector.prevAgent,
			k.inspector.nextAgent, k.inspector.stop, k.inspector.yes, k.inspector.no},
			k.inspector.move.bindings()...)},
		{"resume", append([]key.Binding{k.resume.close, k.resume.resume, k.resume.pane}, k.resume.move.bindings()...)},
		{"a review", append([]key.Binding{k.review.close, k.review.pane, k.review.diff, k.review.file,
			k.review.prevFile, k.review.hunk, k.review.prevHunk, k.review.rng, k.review.comment, k.review.edit,
			k.review.remove, k.review.send, k.review.scope, k.review.reviewer, k.review.prevReview,
			k.review.nextReview, k.review.prevComment, k.review.nextComment, k.review.triage, k.review.viewed,
			k.review.split, k.review.save, k.review.drop, k.review.keep, k.review.discard},
			k.review.move.bindings()...)},
	}
}

// bindings is k's keys, those it has, for /help.
func (k moveKeys) bindings() []key.Binding {
	all := []key.Binding{k.up, k.down, k.pageUp, k.pageDown, k.top, k.bottom}
	return slices.DeleteFunc(all, func(b key.Binding) bool { return len(b.Keys()) == 0 })
}

// bind is a binding labelled label in a hint, doing desc.
func bind(label, desc string, keys ...string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(label, desc))
}

// lettered is the arrows, pages and both ends, with g and G for the ends since
// a laptop may have no home or end, which is safe only where nothing is typed.
func lettered(step, page, top, end string) moveKeys {
	return moveKeys{
		up:       bind("↑", step, "up"),
		down:     bind("↓", step, "down"),
		pageUp:   bind("pgup", page, "pgup"),
		pageDown: bind("pgdn", page, "pgdown"),
		top:      bind("g", top, "home", "g"),
		bottom:   bind("G", end, "end", "G"),
	}
}

// delta is how far msg moves under k, page lines to a page, and false for a key
// that does not move.
func (k moveKeys) delta(msg tea.KeyPressMsg, page int) (int, bool) {
	switch {
	case key.Matches(msg, k.up):
		return -1, true
	case key.Matches(msg, k.down):
		return 1, true
	case key.Matches(msg, k.pageUp):
		return -page, true
	case key.Matches(msg, k.pageDown):
		return page, true
	case key.Matches(msg, k.top):
		return -farAway, true
	case key.Matches(msg, k.bottom):
		return farAway, true
	}
	return 0, false
}

// hint is one item of a key line: keys and what they do, or words alone.
type hint struct{ keys, desc string }

// does says the keys of bs, those enabled, do desc here.
func does(desc string, bs ...key.Binding) hint {
	var labels []string
	for _, b := range bs {
		if b.Enabled() {
			labels = append(labels, b.Help().Key)
		}
	}
	if len(labels) == 0 {
		return hint{}
	}
	return hint{keys: strings.Join(labels, "/"), desc: desc}
}

// twice says b pressed twice does desc.
func twice(desc string, b key.Binding) hint {
	return hint{keys: b.Help().Key + " " + b.Help().Key, desc: desc}
}

// note is words in a key line with no key of their own.
func note(text string) hint { return hint{desc: text} }

// barLine is hints as the status bar draws them, each key in brackets.
func barLine(hs ...hint) string { return strings.Join(barParts(hs...), " · ") }

// barParts is each hint as the bar draws it, for a line that joins them its own way.
func barParts(hs ...hint) []string {
	return hintParts(hs, func(keys string) string {
		return "[" + strings.ReplaceAll(keys, " ", "][") + "]"
	})
}

// boxLine is hints as a modal's key line draws them, keys bare.
func boxLine(hs ...hint) string {
	return strings.Join(hintParts(hs, func(keys string) string { return keys }), " · ")
}

func hintParts(hs []hint, keys func(string) string) []string {
	parts := make([]string, 0, len(hs))
	for _, h := range hs {
		switch {
		case h.keys != "":
			parts = append(parts, keys(h.keys)+" "+h.desc)
		case h.desc != "":
			parts = append(parts, h.desc)
		}
	}
	return parts
}
