package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) confirmBox() string {
	style := styleConfirmAccent
	if m.pendingPre.Dangerous {
		style = styleConfirmDanger
	}
	var b strings.Builder
	tier := m.pendingPre.Mutability
	if tier == "" {
		tier = "unknown scope"
	}
	fmt.Fprintf(&b, "proposed — %s\n\n", mutabilityStyle(tier).Render(tier))
	b.WriteString(lipgloss.NewStyle().Width(max(10, m.width-12)).Render(m.pending.Command) + "\n")
	if m.pending.Rationale != "" {
		fmt.Fprintf(&b, "\n%s %s\n", styleMuted.Render("why:"), m.pending.Rationale)
	}
	if m.pendingPre.Dangerous {
		fmt.Fprintf(&b, "\n%s %s\n", styleDanger.Render("!! look twice !!"), m.pendingPre.RiskNote)
	}
	stepNo := 1
	if m.cur != nil {
		stepNo = len(m.cur.steps) + 1
	}
	fmt.Fprintf(&b, "\n%s\n", styleMuted.Render(fmt.Sprintf("command %d this session · step %d this goal", m.totalCmds+1, stepNo)))
	fmt.Fprintf(&b, "\n%s run   %s stop goal", styleKey.Render("[y]"), styleKey.Render("[n]"))
	// Bottom-centered popup: fixed comfortable width, centered — not a
	// full-bleed strip. History and output stay visible above, which is
	// the context the decision needs.
	w := min(72, max(24, m.width-8))
	return lipgloss.PlaceHorizontal(m.width, lipgloss.Center, style.Width(w).Render(b.String()))
}
