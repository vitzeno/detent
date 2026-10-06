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

func (m Model) inspectorBox() string {
	left, right := m.modalPaneWidths()
	h := m.modalPaneHeight()
	out := m.inspectorOutput()
	start := min(m.insp.output, max(0, len(out)-h))
	body := m.modalPanes(left, right,
		modalPane{title: "output", lines: out[start:min(start+h, len(out))], focused: m.insp.outputFocused},
		modalPane{title: "agent history", lines: m.inspectorHistory(right, h), focused: !m.insp.outputFocused})
	return m.modalBox(m.inspectorTitle(island.Inner(m.modalWidth())), body, boxLine(m.inspectorHints()...))
}

// inspectorTitle names the agent and how it stands, its context on the right.
func (m Model) inspectorTitle(width int) string {
	a := m.insp.agent
	left := styleBrand.Render("agent") + styleFaint.Render(" · ") + toolName.agent.Render(a.name) +
		"  " + m.inspectorStatus()
	right := styleMuted.Render(fmt.Sprintf("ctx %d%%", m.agentContext(a)))
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// inspectorStatus is the title's badge: waiting and for how long, still
// going, or how it ended.
func (m Model) inspectorStatus() string {
	a := m.insp.agent
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

// since is how long ago t was, in whole seconds, as a person counts a wait.
func since(t time.Time) string {
	d := time.Since(t).Truncate(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return status.Dur(d)
}

// inspectorHints offers the answer only while the selected call waits on one.
func (m Model) inspectorHints() []hint {
	k := keymap.inspector
	hs := []hint{does("move", k.move.up, k.move.down), does("pane", k.pane),
		does("agent", k.prevAgent, k.nextAgent), does("stop", k.stop), does("back", k.close),
		does("top/end", k.move.top, k.move.bottom)}
	if m.askOf(m.inspectorRow()) != nil {
		hs = append([]hint{does("run", k.yes), does("decline", k.no)}, hs...)
	}
	return hs
}

// inspectorOutput is the selected row as the output pane draws it: a waiting
// call's command and why it was flagged, live lines, or its view.
func (m Model) inspectorOutput() []string {
	left, _ := m.modalPaneWidths()
	width := island.Inner(left)
	r := m.inspectorRow()
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
		if rendered, ok := m.drawRow(r, width, m.modalPaneHeight(), m.insp.outputFocused); ok {
			return strings.Split(wrapWide(strings.Join(rendered.Lines, "\n"), width), "\n")
		}
	}
	return []string{styleFaint.Render("(no output yet)")}
}

// inspectorHistory is the agent's rows drawn as main history draws them,
// scrolled to keep the cursor in view.
func (m Model) inspectorHistory(paneWidth, height int) []string {
	rows := m.inspectorRows(m.insp.agent)
	// rowLines fits rows to the history pane, so it is handed this one's width.
	m.layout.histColW = paneWidth + railWidth
	focused := m.inspectorRow()
	start, end := listWindow(len(rows), m.insp.cursor, height)
	var out []string
	for _, r := range rows[start:end] {
		out = append(out, m.rowLines(r, focused)...)
	}
	return out
}
