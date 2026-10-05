package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/island"
	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/status"
)

// The inspector's box: the main view's two panes, for one subagent, floated
// over the panes as the finder is.

// inspectorChrome is the box's lines that are not pane content: the title,
// the key line, and each pane's title and border.
const inspectorChrome = 5

func (m Model) inspectorBox() string {
	inner := island.Inner(m.finderWidth())
	left, right := m.inspectorWidths()
	h := m.inspectorHeight()
	out := m.inspectorOutput()
	start := min(m.insp.output, max(0, len(out)-h))
	output := island.Render(styleFaint.Render("output"), paneBorder(m.insp.outputFocused),
		out[start:min(start+h, len(out))], left, h+1)
	history := island.Render(styleFaint.Render("agent history"), paneBorder(!m.insp.outputFocused),
		m.inspectorHistory(right, h), right, h+1)
	lines := append(strings.Split(layout.Row(output, history), "\n"),
		styleFaint.Render(layout.Truncate(m.inspectorKeys(), inner)))
	return island.Render(m.inspectorTitle(inner), palette.Accent, lines, m.finderWidth(), m.finderHeight()-2)
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

// inspectorKeys offers the answer only while the selected call waits on one.
func (m Model) inspectorKeys() string {
	if m.askOf(m.inspectorRow()) != nil {
		return "y run · n decline · ↑↓ move · tab pane · ←→ agent · x stop · esc back"
	}
	return "↑↓ move · tab pane · ←→ agent · x stop · esc back"
}

// inspectorOutput is the selected row as the output pane draws it: a waiting
// call's command and why it was flagged, live lines, or its view.
func (m Model) inspectorOutput() []string {
	left, _ := m.inspectorWidths()
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
		if rendered, ok := m.drawRow(r, width, m.inspectorHeight(), m.insp.outputFocused); ok {
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
	start := max(0, m.insp.cursor-height+1)
	var out []string
	for _, r := range rows[start:min(start+height, len(rows))] {
		out = append(out, m.rowLines(r, focused)...)
	}
	return out
}

// inspectorWidths splits the box as the main view splits the screen.
func (m Model) inspectorWidths() (left, right int) {
	w := layout.Split(island.Inner(m.finderWidth()), bodyWeights, minPaneWidth)
	return w[0], w[1]
}

// inspectorHeight is how many lines each pane shows.
func (m Model) inspectorHeight() int {
	return max(1, m.finderHeight()-2-inspectorChrome)
}
