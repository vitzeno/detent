package ui

import (
	"fmt"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/layout"
)

// The two questions the engine can put to a human, drawn in the
// bottom zone where the input normally is.

// confirmBox shows the exact call text. The literal command is the
// thing being approved, so it is never truncated to fit.
func (m Model) confirmBox() string {
	a := m.asking
	if a == nil {
		return ""
	}
	w := m.layout.width - 4
	var b strings.Builder
	b.WriteString(styleDanger.Render("!! look twice") + "\n")
	for _, line := range wrapPlain(event.Command(a.Tool, a.Args), w) {
		b.WriteString("  " + styleGoal.Render(line) + "\n")
	}
	if a.Rationale != "" {
		b.WriteString("  " + styleFaint.Render(layout.Truncate(a.Rationale, w)) + "\n")
	}
	b.WriteString(styleFaint.Render("  [y/enter] run   [n] skip this call"))
	return b.String()
}

// questionBox is the bound and undo prompts, which are questions
// about the session rather than about one call.
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
		return styleCaution.Render("⚠ undo — your own files") + "\n" +
			styleFaint.Render("  [n/enter] container only   [y] revert your files too   [esc] cancel")
	case modeForget:
		return styleDanger.Render("⚠ delete — this cannot be undone") + "\n" +
			styleFaint.Render("  [y] delete   [n/enter/esc] cancel")
	}
	return ""
}
