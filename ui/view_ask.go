package ui

import (
	"fmt"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/layout"
)

// The questions put to a human, drawn in the bottom zone where the
// input normally is.

// confirmBox shows the exact call text. The literal command is the
// thing being approved, so a tall one scrolls rather than being cut.
func (m Model) confirmBox() string {
	a := m.asking
	if a == nil {
		return ""
	}
	lines, room := m.confirmLines()
	top := confirmTop(m.confirm.top, len(lines), room)
	end := min(top+room, len(lines))
	var b strings.Builder
	b.WriteString(styleDanger.Render("!! look twice") + "\n")
	for _, line := range lines[top:end] {
		b.WriteString("  " + styleGoal.Render(line) + "\n")
	}
	if len(lines) > room {
		where := fmt.Sprintf("lines %d-%d of %d", top+1, end, len(lines))
		if below := len(lines) - end; below > 0 {
			b.WriteString(styleCaution.Render(fmt.Sprintf("  ▼ %d more below · %s", below, where)) + "\n")
		} else {
			b.WriteString(styleFaint.Render("  ▲ end of command · "+where) + "\n")
		}
	}
	if a.Rationale != "" {
		b.WriteString("  " + styleFaint.Render(layout.Truncate(a.Rationale, m.layout.width-4)) + "\n")
	}
	if !m.confirmReady() {
		b.WriteString(styleFaint.Render("  [↓/pgdn] read to the end before it can run   [n] skip this call"))
		return b.String()
	}
	b.WriteString(styleFaint.Render("  [y/enter] run   [n] skip this call"))
	return b.String()
}

// confirmLines is the command as the box wraps it, and how many of its
// lines fit while the panes keep their floor and the box stays on screen.
func (m Model) confirmLines() (lines []string, room int) {
	a := m.asking
	if a == nil {
		return nil, 0
	}
	lines = wrapPlain(layout.Printable(event.Command(a.Tool, a.Args)), m.layout.width-4)
	chrome := 2 // the warning and the keys
	if a.Rationale != "" {
		chrome++
	}
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
			styleFaint.Render("  "+m.undoKeys("   "))
	case modeForget:
		return styleDanger.Render("⚠ delete — this cannot be undone") + "\n" +
			styleFaint.Render("  [y] delete   [n/enter/esc] cancel")
	default:
		return ""
	}
}
