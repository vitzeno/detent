package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/island"
	"github.com/vitzeno/detent/ui/status"
)

// The inspector's box: the main view's two panes, for one subagent.

func (in *inspectorModal) box(m Model) string {
	left, right := m.modalPaneWidths()
	h := m.modalPaneHeight()
	out := in.outputLines(m)
	start := min(in.output, max(0, len(out)-h))
	body := m.modalPanes(left, right,
		modalPane{title: "output", lines: out[start:min(start+h, len(out))], focused: in.outputFocused},
		modalPane{title: "agent history", lines: in.historyLines(m, right, h), focused: !in.outputFocused})
	return m.modalBox(in.title(m, island.Inner(m.modalWidth())), body, boxLine(in.hints(m)...))
}

// title names the agent and how it stands, its context on the right.
func (in *inspectorModal) title(m Model, width int) string {
	a := in.agent
	left := styleBrand.Render("agent") + styleFaint.Render(" · ") + toolName.agent.Render(a.name) +
		"  " + in.status(m)
	right := styleMuted.Render(fmt.Sprintf("ctx %d%%", m.agentContext(a)))
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// status is the title's badge: waiting and for how long, still going, or how it ended.
func (in *inspectorModal) status(m Model) string {
	a := in.agent
	switch {
	case m.blocked(a):
		return styleCaution.Render("! blocked " + since(a.waitingSince))
	case a.ended:
		return m.agentGlyph(a) + " " + styleMuted.Render(m.endedDetail(a))
	case a.stopping:
		return m.agentGlyph(a) + " " + styleCaution.Render(stoppingNote)
	case a.steps == 0:
		return styleFaint.Render("queued")
	}
	return m.spinner.View() + styleMuted.Render(fmt.Sprintf(" %d steps · %d calls", a.steps, a.calls))
}

// hints offers the answer only while the selected call waits on one.
func (in *inspectorModal) hints(m Model) []hint {
	k := keymap.inspector
	hs := []hint{does("move", k.move.up, k.move.down), does("pane", k.pane),
		does("agent", k.prevAgent, k.nextAgent), does("stop", k.stop), does("back", k.close),
		does("top/end", k.move.top, k.move.bottom)}
	if m.askOf(in.row(m)) != nil {
		hs = append([]hint{does("run", k.yes), does("decline", k.no)}, hs...)
	}
	return hs
}

// outputLines is the selected row as the output pane draws it: a waiting
// call's command and why it was flagged, live lines, or its view.
func (in *inspectorModal) outputLines(m Model) []string {
	left, _ := m.modalPaneWidths()
	width := island.Inner(left)
	r := in.row(m)
	if q := m.askOf(r); q != nil {
		lines := wrapPlain(inspectorCommand(q), width)
		if q.Rationale != "" {
			lines = append(lines, "", styleCaution.Render(flaggedLabel)+styleGoal.Render(q.Rationale))
		}
		return lines
	}
	switch {
	case r == nil:
	case r.running:
		return append([]string(nil), r.live...)
	case r.drawable():
		if rendered, ok := m.drawRow(r, width, m.modalPaneHeight(), in.outputFocused); ok {
			return strings.Split(wrapWide(strings.Join(rendered.Lines, "\n"), width), "\n")
		}
	}
	return []string{styleFaint.Render("(no output yet)")}
}

// historyLines is the agent's rows drawn as main history draws them,
// scrolled to keep the cursor in view.
func (in *inspectorModal) historyLines(m Model, paneWidth, height int) []string {
	rows := m.inspectorRows(in.agent)
	// rowLines fits rows to the history pane, so it is handed this one's width.
	m.layout.histColW = paneWidth + railWidth
	focused := in.row(m)
	start, end := listWindow(len(rows), in.cursor, height)
	var out []string
	for _, r := range rows[start:end] {
		out = append(out, m.rowLines(r, focused)...)
	}
	return out
}

// since is how long ago t was, in whole seconds, as a person counts a wait.
func since(t time.Time) string {
	d := time.Since(t).Truncate(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return status.Dur(d)
}
