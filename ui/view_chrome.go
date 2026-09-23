package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/status"
	"github.com/vitzeno/detent/version"
)

// The frame around the panes, in the order it appears on screen: the
// session bar across the top, each pane's own header, and the status
// line above the input bar.

func (m Model) sessionBar() string {
	jev := styleFaint.Render("jev ○ off")
	if m.info.Judge != "" {
		jev = styleSafe.Render("jev ● " + m.info.Judge)
	}
	// Segments, so the bar can shed parts rather than wrap.
	return fitSegments([]string{
		styleBrand.Render("◆ detent " + version.Number),
		styleFaint.Render(m.info.Model),
		styleFaint.Render(fmt.Sprintf("· %d request(s) · %d call(s)", len(m.blocks), m.calls)),
		styleFaint.Render(status.Tokens(m.tokens) + " tok"),
		runModeBadge(m.info.RunMode),
		jev,
	}, m.layout.width)
}

// fitSegments drops the least useful first: name, run mode and judge
// are what a human checks. lipgloss.Width ignores styling, which is
// why this works after rendering.
func fitSegments(segs []string, width int) string {
	keep := make([]bool, len(segs))
	for i := range keep {
		keep[i] = true
	}
	for _, drop := range []int{3, 2, 1} {
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
		// Not the judged kind: nothing here is a file or a command's
		// output. It is the model talking.
		label = "summary"
	case r.kind() != "":
		label = status.KindLabel(r.kind())
	}
	mark, width := "", m.layout.outputColW-24
	if note := viewNote(r); note != "" {
		mark = styleFaint.Render(" " + viewSourceMark + " " + note)
		width -= len(note) + 3
	}
	return fmt.Sprintf("%s %s%s — %s", paneMark(active), paneLabel(label, active), mark,
		styleGoal.Render(layout.Truncate(r.command, width)))
}

// viewSourceMark precedes anything worth saying about how the pane
// was drawn.
const viewSourceMark = "✦"

// viewNote says how the pane was drawn, and usually says nothing.
// Only framing a model had a hand in is worth a word; detent's own
// renderings are the baseline and naming them on every row was noise.
func viewNote(r *callRow) string {
	switch r.viewSource {
	case "composed", "saved":
		return r.viewSource
	}
	return ""
}

// panelName is what the header calls an open page.
func panelName(k panelKind) string {
	switch k {
	case panelUsage:
		return "usage"
	case panelStatus:
		return "status"
	case panelHelp:
		return "help"
	}
	return "detent"
}

func (m Model) historyHeader() string {
	return fmt.Sprintf("%s %s", paneMark(m.nav.focus == focusHistory), paneLabel("history", m.nav.focus == focusHistory))
}

func (m Model) statusBar() string {
	phase := "idle"
	if m.waiting {
		phase = "thinking…"
		for _, b := range m.blocks {
			for _, r := range b.rows {
				if r.running {
					phase = "running…"
				}
			}
		}
	}
	return status.Bar(m.spinner.View(), phase, m.statusHint(),
		status.Notice{Text: m.notice.text, Bad: m.notice.bad}, m.waiting)
}

// statusHint mirrors handleKey's owner() so the hint never falls out of
// sync with what actually routes the keystroke.
func (m Model) statusHint() string {
	if m.mode == modeUndo {
		return "[n/enter] container only · [y] revert your files too · [esc] cancel"
	}
	if m.mode == modeBound {
		return "[y/enter] keep going · [n] stop here"
	}
	switch m.owner() {
	case ownerConfirm:
		return "[y/enter] run · [n] skip this call"
	case ownerOutput:
		// Must match onEscape's actual behavior (keys.go): it aborts a
		// running command here instead of stepping back to history.
		esc := "[esc] history"
		if m.cur != nil {
			esc = "[esc] abort"
		}
		return "[tab] input · [↑/↓] inside · " + esc
	case ownerHistory:
		// esc aborts from here too, and saying so is the difference
		// between a human knowing they can stop a run and thinking
		// they can't.
		if m.cur != nil {
			return "[esc] abort · [tab] output · [↑/↓] move · [space] expand"
		}
		return "[tab] output · [↑/↓] move · [space] expand · [enter] expand"
	case ownerBusy:
		if m.prompt.Open() {
			return "[↑/↓] pick · [tab] complete · [enter] run · [esc] close"
		}
		return "[esc] abort · type / for commands · [tab] history"
	default: // ownerInput
		if m.prompt.Open() {
			return "[↑/↓] pick · [tab] complete · [enter] run · [esc] close"
		}
		return "[tab] history · [enter] run · [" + m.prompt.NewlineKey() + "] newline · type / for cmds"
	}
}

// runModeBadge says where commands actually run. Host is called out
// rather than left implicit: it's the unsandboxed case.
func runModeBadge(mode string) string {
	if mode == "sandbox" {
		return styleSafe.Render("sandbox ●")
	}
	return styleCaution.Render("host ⚠ unsandboxed")
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
