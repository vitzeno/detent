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

// maxSaveDiffLines caps the diff shown in the save confirm — a huge
// diff would otherwise push sizeViewport's bottom-zone measurement to
// swallow the whole screen.
const maxSaveDiffLines = 20

// diffHunksOnly drops the ---/+++ file-path header lines — the box
// title already names the file, and the full path repeated twice
// wraps badly at confirm-box width — keeping the @@ hunk headers and
// the actual +/- lines, which are what the human is here to review.
func diffHunksOnly(diff string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSuffix(diff, "\n"), "\n") {
		if strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "+++ ") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// saveConfirmBox shows what a direct file write would change — a diff,
// not a command — because this write is the harness's own action, not
// something the model proposed; see internal/editfile and
// Driver.RecordFileSave.
func (m Model) saveConfirmBox() string {
	if m.saveRow == nil || m.saveRow.editor == nil {
		return ""
	}
	e := m.saveRow.editor
	lines := diffHunksOnly(e.Diff())
	more := 0
	if len(lines) > maxSaveDiffLines {
		more = len(lines) - maxSaveDiffLines
		lines = lines[:maxSaveDiffLines]
	}

	w := min(90, max(24, m.width-8))
	var b strings.Builder
	fmt.Fprintf(&b, "save changes — %s\n\n", styleGoal.Render(truncateWidth(e.Path, w-18)))
	for _, l := range lines {
		b.WriteString(styleDiffLine(l) + "\n")
	}
	if more > 0 {
		fmt.Fprintf(&b, "%s\n", styleFaint.Render(fmt.Sprintf("… +%d more lines", more)))
	}
	fmt.Fprintf(&b, "\n%s save   %s keep editing", styleKey.Render("[y]"), styleKey.Render("[n]"))

	return lipgloss.PlaceHorizontal(m.width, lipgloss.Center, styleConfirmAccent.Width(w).Render(b.String()))
}
