package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// runModeTag renders the sandbox/host indicator; emphasized when a
// Dangerous command has no sandbox isolation.
func runModeTag(mode string, dangerous bool) string {
	switch mode {
	case "sandbox":
		return styleSafe.Render("sandboxed")
	case "host":
		if dangerous {
			return styleDanger.Render("⚠ host, unsandboxed")
		}
		return styleMuted.Render("host")
	default:
		return ""
	}
}

func (m Model) confirmBox() string {
	style := styleConfirmAccent
	if m.confirm.pre.Dangerous {
		style = styleConfirmDanger
	}
	tier := m.confirm.pre.Mutability
	if tier == "" {
		tier = "unknown scope"
	}

	// Command is the one thing here that matters; everything else is
	// context for it, so rationale sits directly under it, not floated.
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s  %s\n\n", styleMuted.Render("proposed"), mutabilityStyle(tier).Render(tier),
		runModeTag(m.confirm.pre.RunMode, m.confirm.pre.Dangerous))
	b.WriteString(styleGoal.Width(max(10, m.layout.width-12)).Render(m.confirm.pending.Command) + "\n")
	if m.confirm.pending.Rationale != "" {
		fmt.Fprintf(&b, "%s %s\n", styleMuted.Render("why:"), m.confirm.pending.Rationale)
	}
	if m.confirm.pre.Dangerous {
		fmt.Fprintf(&b, "\n%s %s\n", styleDanger.Render("⚠ look twice"), m.confirm.pre.RiskNote)
	}
	stepNo := 1
	if m.cur != nil {
		stepNo = len(m.cur.steps) + 1
	}
	fmt.Fprintf(&b, "\n%s\n", styleMuted.Render(fmt.Sprintf("command %d this session · step %d this goal", m.totalCmds+1, stepNo)))
	fmt.Fprintf(&b, "%s run   %s stop goal", styleKey.Render("[y/enter]"), styleKey.Render("[n]"))
	// Fixed-width popup, not a full-bleed strip, so history and output
	// stay visible above for context.
	w := min(72, max(24, m.layout.width-8))
	return lipgloss.PlaceHorizontal(m.layout.width, lipgloss.Center, style.Width(w).Render(b.String()))
}

// maxSaveDiffLines caps the diff shown, or a huge one eats the screen.
const maxSaveDiffLines = 20

// diffHunksOnly drops the ---/+++ path header lines — the box title
// already names the file — keeping the @@ hunks and +/- lines that
// actually matter for review.
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

// saveConfirmBox shows a diff, not a command — this write is the
// harness's own action, not something the model proposed.
func (m Model) saveConfirmBox() string {
	if m.save.row == nil || m.save.row.editor == nil {
		return ""
	}
	e := m.save.row.editor
	lines := diffHunksOnly(e.Diff())
	more := 0
	if len(lines) > maxSaveDiffLines {
		more = len(lines) - maxSaveDiffLines
		lines = lines[:maxSaveDiffLines]
	}

	w := min(90, max(24, m.layout.width-8))
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n\n", styleMuted.Render("save changes"), styleGoal.Render(truncateWidth(e.Path, w-20)))
	for _, l := range lines {
		b.WriteString(styleDiffLine(l) + "\n")
	}
	if more > 0 {
		fmt.Fprintf(&b, "%s\n", styleFaint.Render(fmt.Sprintf("… +%d more lines", more)))
	}
	fmt.Fprintf(&b, "\n%s save   %s keep editing", styleKey.Render("[y/enter]"), styleKey.Render("[n]"))

	return lipgloss.PlaceHorizontal(m.layout.width, lipgloss.Center, styleConfirmAccent.Width(w).Render(b.String()))
}
