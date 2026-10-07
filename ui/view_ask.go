package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/defuse"
	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/island"
	"github.com/vitzeno/detent/ui/layout"
)

// The questions put to a human, drawn in the bottom zone where the
// input normally is.

// confirmBox shows the exact call text. The literal command is the
// thing being approved, so a tall one scrolls rather than being cut.
func (m Model) confirmBox() string {
	a := m.asking()
	if a == nil {
		return ""
	}
	inner := island.Inner(m.layout.width)
	lines, room := m.confirmLines()
	top := confirmTop(m.confirm.top, len(lines), room)
	end := min(top+room, len(lines))
	body := []string{m.confirmTitle(a)}
	for _, line := range lines[top:end] {
		// Padded, so the command reads as one panel apart from history.
		body = append(body, styleCommand.Render(line+strings.Repeat(" ", max(0, inner-lipgloss.Width(line)))))
	}
	if len(lines) > room {
		where := fmt.Sprintf("lines %d-%d of %d", top+1, end, len(lines))
		if below := len(lines) - end; below > 0 {
			body = append(body, styleCaution.Render(fmt.Sprintf("▼ %d more below · %s", below, where)))
		} else {
			body = append(body, styleFaint.Render("▲ end of command · "+where))
		}
	}
	for i, l := range m.rationaleLines() {
		label := strings.Repeat(" ", len(flaggedLabel))
		if i == 0 {
			label = styleCaution.Render(flaggedLabel)
		}
		body = append(body, label+styleGoal.Render(l))
	}
	if !m.confirmReady() {
		body = append(body, keyHints("[↓/pgdn]", "read to the end before it can run", "[n]", "skip"))
	} else {
		body = append(body, keyHints("[y/enter]", "run", "[n]", "skip"))
	}
	return island.Render("", palette.Danger, body, m.layout.width, len(body))
}

// confirmTitle names what is asked: the tool in its history colour, and
// where this question stands when more wait behind it.
func (m Model) confirmTitle(a *event.ApprovalAsked) string {
	tool := styleGoal.Render(defuse.Text(string(a.Tool)))
	if r := m.row(a.ToolCall); r != nil {
		if style, ok := toolStyle(r); ok {
			tool = style.Render(defuse.Text(string(a.Tool)))
		}
	}
	title := styleDanger.Render("approve") + styleFaint.Render(" · ") + tool
	if n := len(m.asked); n > 1 {
		title += styleFaint.Render(fmt.Sprintf(" · 1 of %d", n))
	}
	return title
}

// keyHints draws key and label pairs, the key bright and its label faint.
func keyHints(pairs ...string) string {
	parts := make([]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, styleBrand.Render(pairs[i])+" "+styleFaint.Render(pairs[i+1]))
	}
	return strings.Join(parts, "   ")
}

// confirmLines is the command as the box wraps it, and how many of its
// lines fit while the panes keep their floor and the box stays on screen.
func (m Model) confirmLines() (lines []string, room int) {
	a := m.asking()
	if a == nil {
		return nil, 0
	}
	lines = wrapPlain(defuse.Text(event.Command(a.Tool, a.Args)), island.Inner(m.layout.width))
	// The border, title, keys and rationale, counted as drawn so the
	// read-to-the-end gate measures what is really on screen.
	chrome := 4 + len(m.rationaleLines())
	room = m.layout.height - 2 - islandOverhead - minBodyRows - chrome
	if len(lines) > room {
		room-- // the line saying how much is left
	}
	return lines, max(1, room)
}

// confirmTop clamps a scroll position to the command's lines.
func confirmTop(top, n, room int) int {
	return max(0, min(top, n-room))
}

// confirmReady is whether the whole command has been on screen, which
// is the only time y may run it.
func (m Model) confirmReady() bool {
	lines, room := m.confirmLines()
	if lines == nil {
		return false
	}
	return m.confirm.seenEnd || confirmTop(m.confirm.top, len(lines), room)+room >= len(lines)
}

// scrollConfirm moves through a tall command, marking it read once the
// last line has been on screen.
func (m *Model) scrollConfirm(delta int) {
	lines, room := m.confirmLines()
	m.confirm.top = confirmTop(m.confirm.top+delta, len(lines), room)
	if m.confirm.top+room >= len(lines) {
		m.confirm.seenEnd = true
	}
}

// questionBox is the bound, undo and delete prompts, which are
// questions about the session rather than about one call.
func (m Model) questionBox() string {
	switch m.mode {
	case modeBound:
		n := 0
		if m.bound != nil {
			n = m.bound.Steps
		}
		return styleCaution.Render(fmt.Sprintf("⚠ %d steps and still going", n)) + "\n" +
			styleFaint.Render("  [y/enter] keep going   [n] stop here")
	case modeUndo:
		return styleCaution.Render("⚠ undo") + "\n" +
			styleFaint.Render("  "+strings.Join(barParts(m.undoHints()...), "   "))
	case modeForget:
		return styleDanger.Render("⚠ delete — this cannot be undone") + "\n" +
			styleFaint.Render("  [y] delete   [n/enter/esc] cancel")
	default:
		return ""
	}
}

// maxRationale caps why a call was flagged, so it cannot crowd out the command.
const maxRationale = 3

// flaggedLabel heads the rationale, which is indented to match it.
const flaggedLabel = "flagged  "

// rationaleLines is why the call was flagged, wrapped beside its label.
func (m Model) rationaleLines() []string {
	a := m.asking()
	if a == nil || a.Rationale == "" {
		return nil
	}
	width := max(1, island.Inner(m.layout.width)-len(flaggedLabel))
	lines := wrapPlain(defuse.Text(a.Rationale), width)
	if len(lines) > maxRationale {
		lines = lines[:maxRationale]
		lines[maxRationale-1] = layout.Truncate(lines[maxRationale-1]+" …", width)
	}
	return lines
}
