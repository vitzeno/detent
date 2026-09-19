package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/vitzeno/detent/internal/loop"
)

// contentWidth is the one width every panel uses — picking it in one
// place is what keeps every screen's border lining up as you move
// between them, instead of each view guessing its own.
func (m Model) contentWidth() int {
	w := m.width - 6
	switch {
	case w > 78:
		return 78
	case w < 36:
		return 36
	default:
		return w
	}
}

// header is the one-line brand anchor shown above every screen's panel —
// the same "detent" wordmark, appearing at the same position, is what
// makes screen-to-screen transitions read as one app rather than a
// sequence of unrelated debug dumps.
func (m Model) header() string {
	return styleAccent.Render("◆ detent")
}

// frame wraps one screen's content in the shared header + a neutral
// bordered panel, so every screen (not just Input/Confirm) gets the same
// "designed" treatment instead of floating loose text.
func (m Model) frame(panel lipgloss.Style, content string) string {
	body := panel.Width(m.contentWidth()).Render(content)
	return "\n" + m.header() + "\n\n" + body + "\n"
}

func (m Model) View() string {
	var content string
	switch m.screen {
	case screenInput:
		content = m.viewInput()
	case screenRunning:
		content = m.viewRunning()
	case screenConfirm:
		content = m.viewConfirm()
	case screenAmbiguous:
		content = m.viewAmbiguous()
	case screenFinished:
		content = m.viewFinished()
	default:
		return ""
	}

	// Every screen's content is already a fixed, known width (contentWidth
	// + the panel border) — centering it as one block, rather than per
	// screen, is what keeps the frame in the same horizontal position as
	// you move between screens instead of each one drifting independently.
	if m.width <= 0 {
		return content
	}
	return lipgloss.PlaceHorizontal(m.width, lipgloss.Center, content)
}

func (m Model) viewInput() string {
	sub := styleFaint.Render("a natural-language goal, worked toward one capability at a time")
	box := styleInputBoxFocus.Width(m.contentWidth()).Render(m.input.View())
	hint := styleHint.Render("enter to run · ctrl+c to quit")

	body := lipgloss.JoinVertical(lipgloss.Left, sub, "", box, "", hint)
	return "\n" + m.header() + "\n\n" + body + "\n"
}

func (m Model) viewRunning() string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s %s\n", styleMuted.Render("goal ·"), styleGoal.Render(m.run.State().Goal))
	b.WriteString(divider(m.contentWidth() - 4))
	b.WriteString("\n")

	findings := m.run.State().Findings
	if len(findings) == 0 && !m.waiting {
		b.WriteString(styleFaint.Render("  (nothing yet)") + "\n")
	}
	for _, f := range findings {
		fmt.Fprintf(&b, "  %s %-2d %-22s %s\n",
			styleStepDone.Render("✓"), f.Step, f.Action, styleMuted.Render(summarizeFacts(f.Facts)))
	}

	if m.waiting {
		label := m.active
		if label == "thinking" {
			label = "thinking…"
		} else {
			label += "…"
		}
		fmt.Fprintf(&b, "  %s %s %s\n", m.spinner.View(), styleStepPending.Render("▸"), styleStepPending.Render(label))
	}

	b.WriteString("\n")
	b.WriteString(m.viewBudgets())
	b.WriteString("\n\n")
	b.WriteString(styleHint.Render("[v] findings  [esc] abort"))

	if m.showFindings {
		b.WriteString("\n\n")
		b.WriteString(m.viewFindingsInline())
	}

	return m.frame(stylePanel, b.String())
}

// divider is a thin horizontal rule — the kind of small structural device
// that gives a bordered panel internal hierarchy (a header row, a body)
// instead of one undifferentiated block of text.
func divider(width int) string {
	if width < 1 {
		width = 1
	}
	return styleFaint.Render(strings.Repeat("─", width))
}

// viewBudgets renders both budget bars — animated fills (progress.Model
// ticks its own FrameMsg toward the target percent), not static text,
// so a step or write actually visibly happening reads as progress
// (§8: "nothing streams — this view is what replaces it").
func (m Model) viewBudgets() string {
	steps, writes := 0, 0
	if m.run != nil {
		steps = m.run.StepsUsed()
		writes = m.run.WritesUsed()
	}
	stepBudget := m.l.Budgets.Steps
	writeBudget := m.l.Budgets.Writes

	stepLine := fmt.Sprintf("steps  %s %d/%d", m.stepProgress.ViewAs(ratio(steps, stepBudget)), steps, stepBudget)
	writeLine := fmt.Sprintf("writes %s %d/%d", m.writeProgress.ViewAs(ratio(writes, writeBudget)), writes, writeBudget)
	return styleMuted.Render(stepLine) + "\n" + styleMuted.Render(writeLine)
}

func ratio(used, budget int) float64 {
	if budget <= 0 {
		return 0
	}
	r := float64(used) / float64(budget)
	if r > 1 {
		r = 1
	}
	return r
}

// viewFindingsInline renders reduced facts under the [v] toggle — plain
// indented text within the enclosing panel, not a second nested border
// (a box inside a box reads as clutter, not hierarchy).
func (m Model) viewFindingsInline() string {
	findings := m.run.State().Findings
	if len(findings) == 0 {
		return styleFaint.Render("  (nothing yet)")
	}
	var lines []string
	lines = append(lines, styleMuted.Render("findings")+"\n"+divider(m.contentWidth()-4))
	for _, f := range findings {
		lines = append(lines, styleAccent.Render(fmt.Sprintf("  step %d · %s", f.Step, f.Action)))
		lines = append(lines, renderFacts(f.Facts, "    "))
	}
	return strings.Join(lines, "\n")
}

// maxFactRows caps how many rows of an already-reduced list (paths,
// processes) this detail view shows before summarizing the rest as
// "+N more" — a display-only bound on data reduce already produced, not
// a second round of reduction (§4.4's rule is about raw command output,
// never touched here).
const maxFactRows = 8

// renderFacts formats one step's reduced Facts for the human detail view
// — real structure for the shapes reduce's own reducers produce, JSON for
// anything else, but never Go's bare %v map syntax (unreadable: unordered
// keys, no line breaks, nested maps run together on one line).
func renderFacts(facts map[string]any, indent string) string {
	if facts == nil {
		return styleFaint.Render(indent + "(no facts)")
	}

	if procs, ok := facts["processes"].([]map[string]any); ok {
		return indent + renderRows(procs, func(p map[string]any) string {
			return fmt.Sprintf("pid %v  %-10v %v", p["pid"], p["owner"], p["cmd"])
		}, factsTotal(facts, len(procs)), indent)
	}
	if paths, ok := facts["paths"].([]string); ok {
		return indent + renderRows(paths, func(p string) string {
			return p
		}, factsTotal(facts, len(paths)), indent)
	}
	if preview, ok := facts["preview"].(string); ok {
		lines := []string{fmt.Sprintf("%v bytes · %v lines", facts["bytes"], facts["lines"])}
		for l := range strings.SplitSeq(preview, "\n") {
			lines = append(lines, indent+styleFaint.Render(l))
		}
		return strings.Join(lines, "\n")
	}
	if out, ok := facts["output"].(string); ok {
		return indent + out
	}

	b, err := json.MarshalIndent(facts, indent, "  ")
	if err != nil {
		return indent + styleFaint.Render("(unreadable facts)")
	}
	return indent + string(b)
}

// factsTotal prefers the reducer's own "total_found" (set when it
// truncated — reduce.ProcessLines' doc comment explains why) over the
// visible row count, so "+N more" reflects reality even when the reducer
// already dropped rows before this view ever saw them.
func factsTotal(facts map[string]any, visible int) int {
	if total, ok := facts["total_found"].(int); ok {
		return total
	}
	return visible
}

func renderRows[T any](rows []T, format func(T) string, total int, indent string) string {
	if len(rows) == 0 {
		return styleFaint.Render("(none)")
	}
	shown := rows
	if len(shown) > maxFactRows {
		shown = shown[:maxFactRows]
	}
	var lines []string
	for _, r := range shown {
		lines = append(lines, format(r))
	}
	rest := total - len(shown)
	if rest > 0 {
		lines = append(lines, styleFaint.Render(fmt.Sprintf("… +%d more", rest)))
	}
	return strings.Join(lines, "\n"+indent)
}

func (m Model) viewConfirm() string {
	req := m.pending.Confirm
	var b strings.Builder

	fmt.Fprintf(&b, "%s %s\n", styleMuted.Render("goal ·"), styleGoal.Render(req.Goal))
	b.WriteString(divider(m.contentWidth() - 4))
	b.WriteString("\n\n")

	if len(req.Done) == 0 {
		b.WriteString(styleFaint.Render("done so far: (nothing yet)"))
	} else {
		b.WriteString(styleMuted.Render("done so far"))
		b.WriteString("\n")
		for _, f := range req.Done {
			fmt.Fprintf(&b, "  %d. %s\n", f.Step, f.Action)
		}
	}
	b.WriteString("\n")

	tierLabel := dangerStyle(string(req.Danger)).Render(strings.ToUpper(string(req.Danger)))
	fmt.Fprintf(&b, "next — %s\n  %s — %s\n\n", tierLabel, styleAccent.Render(req.Capability), req.TargetDesc)
	fmt.Fprintf(&b, "%s\n\n", styleMuted.Render(fmt.Sprintf(
		"write %d of %d allowed · step %d of %d", req.WritesUsed+1, req.WriteBudget, req.Step, req.StepBudget)))

	b.WriteString(styleKey.Render("[y]") + " run   " + styleKey.Render("[n]") + " stop run   " +
		styleKey.Render("[v]") + " view findings")

	return m.frame(stylePanelDanger, b.String())
}

func (m Model) viewAmbiguous() string {
	amb := m.pending.Ambiguous
	var b strings.Builder

	fmt.Fprintf(&b, "%s %s\n", styleMuted.Render("goal ·"), styleGoal.Render(m.run.State().Goal))
	b.WriteString(divider(m.contentWidth() - 4))
	b.WriteString("\n\n")

	done := m.run.State().Findings
	if len(done) == 0 {
		b.WriteString(styleFaint.Render("done so far: (nothing yet)") + "\n\n")
	} else {
		b.WriteString(styleMuted.Render("done so far") + "\n")
		for _, f := range done {
			fmt.Fprintf(&b, "  %d. %s\n", f.Step, f.Action)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "which %s?\n\n", amb.ArgType)

	for i, c := range amb.Candidates {
		p := amb.Probabilities[c.ID]
		cursor := "  "
		desc := styleGoal.Render(c.Desc)
		if i == m.ambiguousCursor {
			cursor = styleAccent.Render("▸ ")
			desc = styleAccent.Render(c.Desc)
		}
		fmt.Fprintf(&b, "%s%-38s %s %s\n", cursor, desc, probabilityBar(p, 12), styleMuted.Render(fmt.Sprintf("%.2f", p)))
	}

	if m.waiting {
		fmt.Fprintf(&b, "\n  %s %s\n", m.spinner.View(), styleStepPending.Render("resolving…"))
	} else {
		b.WriteString("\n" + styleHint.Render("[↑/↓] choose  [enter] select  [esc] abort run"))
	}

	return m.frame(stylePanel, b.String())
}

func probabilityBar(p float64, width int) string {
	filled := min(int(p*float64(width)), width)
	return styleAccent.Render(strings.Repeat("█", filled)) + styleFaint.Render(strings.Repeat("░", width-filled))
}

func (m Model) viewFinished() string {
	var b strings.Builder

	goal := ""
	var findings []loop.Finding
	if m.run != nil {
		goal = m.run.State().Goal
		findings = m.run.State().Findings
	}

	symbol, label, style := finishedBadge(m)
	fmt.Fprintf(&b, "%s %s  %s\n", styleMuted.Render("goal ·"), styleGoal.Render(goal), style.Render(symbol))
	b.WriteString(divider(m.contentWidth() - 4))
	b.WriteString("\n\n")

	if m.fatalErr != nil {
		fmt.Fprintf(&b, "%s\n\n", styleDanger.Render("error: "+m.fatalErr.Error()))
	} else {
		fmt.Fprintf(&b, "%s\n\n", style.Render(label))
	}

	for _, f := range findings {
		fmt.Fprintf(&b, "  %d  %-22s %s\n", f.Step, f.Action, styleMuted.Render(summarizeFacts(f.Facts)))
	}
	if len(findings) == 0 && m.fatalErr == nil {
		b.WriteString(styleFaint.Render("  nothing was run.") + "\n")
	}

	if m.run != nil && m.run.WritesUsed() > 0 {
		fmt.Fprintf(&b, "\n%s\n", styleMuted.Render(fmt.Sprintf(
			"%d step%s · %d write%s", len(findings), plural(len(findings)), m.run.WritesUsed(), plural(m.run.WritesUsed()))))
	} else if len(findings) > 0 {
		fmt.Fprintf(&b, "\n%s\n", styleMuted.Render(fmt.Sprintf("%d step%s", len(findings), plural(len(findings)))))
	}

	b.WriteString("\n")
	b.WriteString(styleHint.Render("[v] findings  [enter] new goal  [q] quit"))

	if m.showFindings {
		b.WriteString("\n\n")
		b.WriteString(m.viewFindingsInline())
	}

	return m.frame(stylePanel, b.String())
}

func finishedBadge(m Model) (symbol, label string, style lipgloss.Style) {
	if m.fatalErr != nil {
		return "✗", "an internal error stopped the run", styleDanger
	}
	if m.aborted {
		return "⚠", "aborted by user", styleCaution
	}
	if m.termination == nil {
		return "✗", "stopped unexpectedly", styleDanger
	}
	switch m.termination.Reason {
	case loop.ReasonGoalAchieved, loop.ReasonDone:
		return "✓", "done", styleSafe
	case loop.ReasonCannotProceed, loop.ReasonGoalUnsatisfiable:
		return "✗", "no available capability can make progress on this goal", styleDanger
	case loop.ReasonStepBudget:
		return "⚠", "stopped: step budget · goal not confirmed", styleCaution
	case loop.ReasonStateBudget:
		return "⚠", "stopped: state budget reached", styleCaution
	case loop.ReasonWriteBudget:
		return "⚠", "stopped: write budget reached", styleCaution
	case loop.ReasonLowConfidence:
		return "⚠", "stopped: not confident enough to guess — " + m.termination.Detail, styleCaution
	case loop.ReasonAmbiguousTarget:
		return "⚠", "stopped: target was ambiguous — " + m.termination.Detail, styleCaution
	case loop.ReasonDeclined:
		return "✗", "declined — nothing was run", styleDanger
	default:
		return "✗", string(m.termination.Reason), styleDanger
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
