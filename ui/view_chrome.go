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
	return status.Bar(m.spinner.View(), phase, m.statusHint(),
		status.Notice{Text: m.notice.text, Bad: m.notice.bad}, m.waiting)
}

// statusHint mirrors handleKey: the three questions it intercepts
// first, then owner(), so the hint names what the key will do.
func (m Model) statusHint() string {
	// Held behind what was typed, so say it is there and how to reach it.
	if m.mode == modeInput && (m.asking() != nil || m.bound != nil) {
		return "a question is waiting · send or clear what you typed to see it"
	}
	if m.mode == modeFinder {
		return "[enter] jump · [ctrl+f] kind · [↑/↓] move · [esc] back"
	}
	if m.mode == modeUndo {
		return m.undoKeys(" · ")
	}
	if m.mode == modeBound {
		return "[y/enter] keep going · [n] stop here"
	}
	if m.mode == modeForget {
		return "[y] delete · [↑/↓] read · any other key cancels"
	}
	switch m.owner() {
	case ownerConfirm:
		if !m.confirmReady() {
			return "[↓/pgdn] read the rest · [n] skip this tool call"
		}
		return "[y/enter] run · [n] skip this tool call"
	case ownerOutput:
		// Must match onEscape's actual behavior (keys.go): it aborts a
		// running command here instead of stepping back to history.
		esc := "[esc] history"
		if m.cur != nil {
			esc = "[esc] abort"
		}
		return "[tab] input · [↑/↓] inside · " + esc
	case ownerHistory:
		// esc aborts from here too, and a human needs to know they can stop a run.
		if m.cur != nil {
			return "[esc] abort · [tab] output · [↑/↓] move · [space] expand"
		}
		return "[tab] output · [↑/↓] move · [space] expand · [enter] expand"
	case ownerBusy:
		if m.prompt.shell {
			return m.shellHint()
		}
		if m.prompt.Open() {
			return "[↑/↓] pick · [tab] complete · [enter] run · [esc] close"
		}
		return "[esc] abort · [enter] steers · [tab] history"
	default: // ownerInput
		if m.prompt.shell {
			return m.shellHint()
		}
		if m.prompt.Open() {
			return "[↑/↓] pick · [tab] complete · [enter] run · [esc] close"
		}
		return "[tab] history · [enter] run · [" + m.prompt.NewlineKey() + "] newline · type / for cmds"
	}
}

// shellHint is the bar in shell mode, where / is a path.
func (m Model) shellHint() string {
	if m.userCommandRunning() {
		return "[esc] stop · [shift+tab] back to a request"
	}
	return "[shift+tab] request · [enter] run · [tab] history"
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
