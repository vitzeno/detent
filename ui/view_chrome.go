package ui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/status"
	"github.com/vitzeno/detent/version"
)

// The frame around the panes, in screen order: the session bar, each
// pane's header, and the status line above the input bar.

// viewSourceMark precedes anything worth saying about how the pane
// was drawn.
const viewSourceMark = "✦"

func (m Model) sessionBar() string {
	jev := styleFaint.Render("jev ○ off")
	if m.run.Judge != "" {
		jev = styleSafe.Render("jev ● " + m.run.Judge)
	}
	// Segments, so the bar can shed parts rather than wrap.
	return fitSegments([]string{
		styleBrand.Render("◆ detent " + version.Number),
		styleFaint.Render(m.run.Model),
		m.contextStyle().Render(m.contextGauge()),
		runModeBadge(m.runMode()),
		jev,
	}, m.layout.width)
}

// contextGauge is how full the transcript budget is, which is what
// decides when a Turn stalls to compact. Raw totals live in /status.
func (m Model) contextGauge() string {
	budget := m.run.ContextTokens
	if budget <= 0 || m.ctxTokens <= 0 {
		return status.Tokens(m.tokens) + " tok"
	}
	return fmt.Sprintf("ctx %d%%", m.ctxTokens*100/budget)
}

// contextStyle warns before the stall rather than after it: crossing
// the budget costs a summariser round trip mid-Turn.
func (m Model) contextStyle() lipgloss.Style {
	budget := m.run.ContextTokens
	if budget <= 0 || m.ctxTokens <= 0 {
		return styleFaint
	}
	switch pct := m.ctxTokens * 100 / budget; {
	case pct >= 90:
		return styleDanger
	case pct >= 75:
		return styleCaution
	}
	return styleFaint
}

// runModeBadge says where commands actually run. Host is called out
// rather than left implicit, since it is the unsandboxed case.
func runModeBadge(mode string) string {
	if mode == "sandbox" {
		return styleSafe.Render("sandbox ●")
	}
	return styleCaution.Render("host ⚠ unsandboxed")
}

// fitSegments drops the least useful first: name, run mode and judge
// are what a human checks. lipgloss.Width ignores styling.
func fitSegments(segs []string, width int) string {
	keep := make([]bool, len(segs))
	for i := range keep {
		keep[i] = true
	}
	for _, drop := range []int{2, 1} {
		if joinedWidth(segs, keep) <= width {
			break
		}
		keep[drop] = false
	}
	var parts []string
	for i, s := range segs {
		if keep[i] {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "  ")
}

func joinedWidth(segs []string, keep []bool) int {
	n, first := 0, true
	for i, s := range segs {
		if !keep[i] {
			continue
		}
		if !first {
			n += 2
		}
		first = false
		n += lipgloss.Width(s)
	}
	return n
}

// viewportHeader names what the output pane is currently showing: a
// file being edited, a tool, or a command's output by judged kind.
func (m Model) viewportHeader() string {
	active := m.nav.focus == focusOutput
	if m.mode == modeUndo {
		return fmt.Sprintf("%s %s", paneMark(active), paneLabel("undoing", active))
	}
	if m.mode == modeForget {
		return fmt.Sprintf("%s %s", paneMark(active), paneLabel("deleting", active))
	}
	if m.panel.open != panelNone {
		return fmt.Sprintf("%s %s — %s", paneMark(active), paneLabel(panelName(m.panel.open), active),
			styleFaint.Render("esc to close"))
	}
	r := m.focused()
	if r == nil {
		return fmt.Sprintf("%s %s", paneMark(active), paneLabel("detent", active))
	}
	label := "output"
	switch {
	case r.prose != "":
		// The model talking, not a file or a command's output.
		label = "summary"
	case r.kind() != "":
		label = status.KindLabel(r.kind())
	}
	mark, width := "", m.layout.outputColW-24
	if note := viewNote(r); note != "" {
		mark = styleFaint.Render(" " + viewSourceMark + " " + note)
		width -= len(note) + 3
	}
	head := fmt.Sprintf("%s %s%s", paneMark(active), paneLabel(label, active), mark)
	// Prose has no command, and a dangling dash reads as a truncation.
	if r.command == "" {
		return head
	}
	return head + " — " + styleGoal.Render(layout.Truncate(r.command, width))
}

// viewNote says how the pane was drawn, and usually says nothing: only
// framing a model had a hand in is worth a word.
func viewNote(r *historyRow) string {
	switch r.viewSource {
	case "composed", "saved":
		return r.viewSource
	}
	return ""
}

// panelName is what the header calls an open page.
func panelName(k panelKind) string {
	switch k {
	case panelContext:
		return "context"
	case panelStatus:
		return "status"
	case panelHelp:
		return "help"
	case panelSessions:
		return "sessions"
	case panelMCP:
		return "mcp"
	case panelSkills:
		return "skills"
	default:
		return "detent"
	}
}

func (m Model) historyHeader() string {
	return fmt.Sprintf("%s %s", paneMark(m.nav.focus == focusHistory), paneLabel("history", m.nav.focus == focusHistory))
}

func (m Model) statusBar() string {
	phase := "idle"
	switch {
	case m.waiting && slices.ContainsFunc(m.blocks, anyRunning):
		phase = "running…"
	case m.waiting:
		phase = "thinking…"
	}
	// Seen from anywhere, so a child waiting on the human is never missed.
	if running, blocked := m.agentCounts(); running > 0 {
		phase += fmt.Sprintf(" · agents %d", running)
		if blocked > 0 {
			phase += fmt.Sprintf(" · %d !", blocked)
		}
	}
	return status.Bar(m.spinner.View(), phase, m.statusHint(),
		status.Notice{Text: m.notice.text, Bad: m.notice.bad}, m.waiting)
}

// agentCounts is how many subagents are still running, and how many of
// those wait on the human.
func (m Model) agentCounts() (running, blocked int) {
	for _, a := range m.agents {
		if a.ended {
			continue
		}
		running++
		if m.blocked(a) {
			blocked++
		}
	}
	return running, blocked
}

// statusHint mirrors handleKey: the three questions it intercepts first, then
// owner(), so the hint names what each key will do, esc's first wherever it does anything.
func (m Model) statusHint() string {
	// Held behind what was typed, so say it is there and how to reach it.
	if m.mode == modeInput && (m.asking() != nil || m.bound != nil) {
		return "a question is waiting · send or clear what you typed to see it"
	}
	ask := keymap.ask
	switch m.mode {
	case modeModal:
		return m.modal.hint(m)
	case modeReview:
		// The box's key line leads with what esc closes, which the bar repeats.
		return barLine(m.reviewEsc(), note("the review's keys are in the box"))
	case modeUndo:
		return barLine(m.undoHints()...)
	case modeBound:
		return barLine(does("keep going", ask.yes, ask.enter), does("stop here", ask.no))
	case modeForget:
		return barLine(does("delete", ask.yes), does("read", ask.read.up, ask.read.down),
			note("any other key cancels"))
	case modeInput, modeConfirm:
	}
	return barLine(append([]hint{m.escHint()}, m.ownerHints()...)...)
}

// escHint is what esc does now, as onEscape decides it, none where it does nothing.
func (m Model) escHint() hint {
	esc := keymap.app.esc
	switch {
	case m.mode == modeConfirm:
		return does("skips this tool call", esc)
	case m.prompt.Open():
		return does("closes the menu", esc)
	case m.panel.open != panelNone:
		return does("closes the page", esc)
	case m.nav.focus == focusOutput && m.cur == nil:
		return does("back to history", esc)
	case !m.userCommandRunning() && m.cur == nil:
		return hint{}
	}
	what := "the request"
	if m.userCommandRunning() {
		what = "your command"
	}
	if m.escStops() {
		return does("again stops "+what, esc)
	}
	return twice("stops "+what, esc)
}

// ownerHints is the keys of whatever owns them, esc's left to escHint.
func (m Model) ownerHints() []hint {
	ask, in, hist := keymap.ask, keymap.input, keymap.history
	pick := []hint{does("pick", in.pick.up, in.pick.down), does("complete", in.complete), does("run", in.run)}
	switch m.owner() {
	case ownerConfirm:
		if !m.confirmReady() {
			return []hint{does("read the rest", ask.read.down, ask.read.pageDown), does("skip this tool call", ask.no)}
		}
		return []hint{does("run", ask.yes, ask.enter), does("skip this tool call", ask.no)}
	case ownerOutput:
		return []hint{does("input", keymap.app.tab), does("inside", keymap.output.move.up, keymap.output.move.down)}
	case ownerHistory:
		if len(m.pinned()) > 0 {
			return []hint{does("agents", hist.agents), does("move", hist.move.up, hist.move.down), does("open", hist.open)}
		}
		return []hint{does("output", keymap.app.tab), does("move", hist.move.up, hist.move.down),
			does("expand", hist.expand), does("expand", hist.open)}
	case ownerBusy:
		if m.prompt.shell {
			return m.shellHints()
		}
		if m.prompt.Open() {
			return pick
		}
		// A waiting subagent takes no keys, so say how to reach it.
		if _, blocked := m.agentCounts(); blocked > 0 {
			return []hint{note("an agent is waiting on you"), note("[tab], then [a]")}
		}
		return []hint{does("steers", in.run), does("history", keymap.app.tab)}
	case ownerInput:
	}
	if m.prompt.shell {
		return m.shellHints()
	}
	if m.prompt.Open() {
		return pick
	}
	return []hint{does("history", keymap.app.tab), does("run", in.run),
		{keys: m.prompt.NewlineKey(), desc: "newline"}, note("type / for cmds")}
}

// shellHints is the bar in shell mode, where / is a path.
func (m Model) shellHints() []hint {
	if m.userCommandRunning() {
		return []hint{does("back to a request", keymap.app.entry)}
	}
	return []hint{does("request", keymap.app.entry), does("run", keymap.input.run), does("history", keymap.app.tab)}
}

func paneMark(active bool) string {
	if active {
		return styleBrand.Render("●")
	}
	return styleFaint.Render("○")
}

func paneLabel(name string, active bool) string {
	if active {
		return styleBrand.Render(name)
	}
	return styleFaint.Render(name)
}
