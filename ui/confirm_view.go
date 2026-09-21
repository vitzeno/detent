package ui

import (
	"fmt"
	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/render"
	"strings"

	"charm.land/lipgloss/v2"
)

// The two modal boxes over the input bar: a proposed command
// awaiting approval, and a diff awaiting a confirmed file write.

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
	// The model's own prose, dimmed: the command above is what a human
	// is being asked to read, not the reasoning for it.
	if m.confirm.pending.Rationale != "" {
		fmt.Fprintf(&b, "%s %s\n", styleMuted.Render("why:"), styleMuted.Render(m.confirm.pending.Rationale))
	}
	if m.confirm.pre.Dangerous {
		fmt.Fprintf(&b, "\n%s %s\n", styleDanger.Render("⚠ look twice"), styleMuted.Render(m.confirm.pre.RiskNote))
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
	fmt.Fprintf(&b, "%s  %s\n\n", styleMuted.Render("save changes"), styleGoal.Render(layout.Truncate(e.Path, w-20)))
	for _, l := range lines {
		b.WriteString(render.DiffLine(l) + "\n")
	}
	if more > 0 {
		fmt.Fprintf(&b, "%s\n", styleFaint.Render(fmt.Sprintf("… +%d more lines", more)))
	}
	fmt.Fprintf(&b, "\n%s save   %s keep editing", styleKey.Render("[y/enter]"), styleKey.Render("[n]"))

	return lipgloss.PlaceHorizontal(m.layout.width, lipgloss.Center, styleConfirmAccent.Width(w).Render(b.String()))
}

// rollbackConfirmBox asks before writing to the human's own files. It
// names every path, and marks the ones nothing detent ran can account
// for — reverting those destroys work it never made, which is the
// only irreversible thing here.
func (m Model) rollbackConfirmBox() string {
	r := m.rollback
	if r.target == nil {
		return ""
	}
	w := min(90, max(24, m.layout.width-8))

	unseen := 0
	for _, f := range r.files {
		if f.Unseen {
			unseen++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n\n", styleMuted.Render("undo to before"),
		styleGoal.Render(fmt.Sprintf("#%d  %s", r.step, layout.Truncate(r.target.goal, w-24))))
	fmt.Fprintf(&b, "%s\n", styleMuted.Render(fmt.Sprintf(
		"reverting the workspace would change %s:", plural(len(r.files), "file"))))

	files, more := r.files, 0
	if len(files) > maxRollbackFiles {
		more, files = len(files)-maxRollbackFiles, files[:maxRollbackFiles]
	}
	for _, f := range files {
		mark, style := "restore", styleDiffAdd
		if f.Removed {
			mark, style = "delete ", styleDiffDel
		}
		note := ""
		if f.Unseen {
			note = styleDanger.Render("  ⚠ not detent's")
		}
		fmt.Fprintf(&b, "  %s %s%s\n", style.Render(mark),
			styleGoal.Render(layout.Truncate(f.Path, w-28)), note)
	}
	if more > 0 {
		fmt.Fprintf(&b, "%s\n", styleFaint.Render(fmt.Sprintf("  … +%d more", more)))
	}
	if unseen > 0 {
		fmt.Fprintf(&b, "\n%s %s\n", styleDanger.Render("⚠"), styleCaution.Render(fmt.Sprintf(
			"%s changed after detent's last step — reverting throws that away",
			plural(unseen, "file"))))
	}
	fmt.Fprintf(&b, "\n%s container only   %s revert the files too   %s cancel",
		styleKey.Render("[n/enter]"), styleKey.Render("[y]"), styleKey.Render("[esc]"))
	return lipgloss.PlaceHorizontal(m.layout.width, lipgloss.Center, styleConfirmDanger.Width(w).Render(b.String()))
}

// plural counts a thing without the "(s)" hedge.
func plural(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

// maxRollbackFiles caps the list, or a wide-reaching goal fills the
// screen with paths.
const maxRollbackFiles = 12

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
