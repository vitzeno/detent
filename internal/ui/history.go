package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/vitzeno/detent/internal/agentloop"
	"github.com/vitzeno/detent/internal/ui/status"
)

// contPrefix indents a wrapped line's continuation so it reads as one
// paragraph under its label, not flush against the pane edge.
const contPrefix = "    "

// wrapPlain word-wraps plain (unstyled) text to width columns — history
// entries can run long, and truncating them with "…" throws away
// information a human might specifically be looking for (a file's full
// path, a command's full output summary), so history wraps instead of
// truncating; the output pane keeps truncating, where one command's
// detail view has room to scroll instead.
func wrapPlain(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	wrapped := lipgloss.NewStyle().Width(width).Render(s)
	lines := strings.Split(strings.TrimRight(wrapped, "\n"), "\n")
	for i, l := range lines {
		// Width() pads every line to exactly width — trim it back off so
		// callers appending more text (a "· detail" suffix, a style) never
		// have to reason about trailing padding in the middle of a line.
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

// wrapStyled wraps text to width, styling each physical line; the
// first line gets prefix (e.g. "  ✔ ", already inside text), every
// continuation line gets contPrefix instead — so a multi-line banner
// indents the same way a wrapped goal or command does.
func wrapStyled(style lipgloss.Style, text string, width int) []string {
	lines := wrapPlain(text, width)
	out := make([]string, len(lines))
	for i, l := range lines {
		p := contPrefix
		if i == 0 {
			p = "  "
		}
		out[i] = p + style.Render(l)
	}
	return out
}

// divider marks a goal boundary — short and faint rather than a
// full-width rule, so it reads as a break, not another row of content.
func (m Model) divider() string {
	return "  " + styleFaint.Render(strings.Repeat("─", min(20, max(4, m.histColW-8))))
}

// Records the focused row's line for cursor windowing.
func (m *Model) historyLines() []string {
	var lines []string
	rows := m.rows()
	for bi, b := range m.blocks {
		if bi > 0 {
			lines = append(lines, m.divider())
		}
		// A tool block (/tree, /usage, /help, a file opened from a tree)
		// has no goal text of its own — its one step's own line already
		// says what it is, so there's no separate header to wrap here.
		if b.tool == "" {
			for i, gl := range wrapPlain(b.goal, m.histColW-14) {
				if i == 0 {
					lines = append(lines, styleMuted.Render("goal · ")+styleGoal.Render(gl))
				} else {
					lines = append(lines, contPrefix+styleGoal.Render(gl))
				}
			}
		}
		for _, r := range b.steps {
			if r == rows[m.cursorClamped()] && len(rows) > 0 {
				m.cursorLine = len(lines)
			}
			lines = append(lines, m.stepLines(r)...)
			if r.expanded {
				lines = append(lines, previewLines(r, m.histColW-10)...)
			}
		}
		if b.ended && b.tool == "" {
			lines = append(lines, m.goalBanner(b)...)
		} else if b == m.cur && m.waiting && !anyRunning(b) {
			lines = append(lines, fmt.Sprintf("  %s %s", m.spinner.View(), styleFaint.Render("thinking…")))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, styleFaint.Render("  no goals yet — describe one below"))
	}
	return lines
}

func (m *Model) cursorClamped() int {
	rows := m.rows()
	if len(rows) == 0 {
		return -1
	}
	if m.cursor < 0 {
		return 0
	}
	if m.cursor >= len(rows) {
		return len(rows) - 1
	}
	return m.cursor
}

// stepLines renders one step as physical lines: mark+icon+command on
// the first, wrapped command continuation beneath it, then the
// judged/provisional detail appended after the last line — provisional
// styling first, upgraded when judged post lands, both from the status
// component.
func (m Model) stepLines(r *stepRow) []string {
	mark := "  "
	if r == m.focused() {
		mark = styleRowCursor.Render("▸ ")
	}
	// A tool row (or a file opened from a tree) never executed a shell
	// command, so the status.Badge dispatch below — which reads r.ec —
	// doesn't apply; show a plain label instead.
	if r.toolKind != "" {
		cmd := truncateWidth(r.command, m.histColW-14)
		return []string{fmt.Sprintf("%s%s %s", mark, styleMuted.Render("○"), cmd)}
	}
	s := status.Row{}
	if r.running {
		s.Running = true
		s.LiveLines = len(r.live)
		s.Dropped = r.dropped
	} else if r.ec != nil {
		s.HasResult = true
		s.ExitCode = r.ec.Result.ExitCode
		s.Summary = r.ec.Result.Summary()
		if r.ec.Post != nil {
			s.Judged = true
			s.Status = r.ec.Post.Status
			s.Attention = r.ec.Post.Attention
		}
	}
	icon, detail := status.Badge(s, m.spinner.View())

	// The command itself stays truncated, not wrapped: unlike a goal
	// or summary sentence, a command is often one unbreakable token
	// (a path, a flag string) with no space for word-wrap to break at
	// — wrapping it produces ugly mid-word fragmentation, so a clean
	// "…" reads better here. The row stays one physical line.
	cmd := truncateWidth(r.command, m.histColW-34)
	return []string{fmt.Sprintf("%s%s %s %s", mark, icon, cmd, styleMuted.Render("· "+detail))}
}

func anyRunning(b *goalBlock) bool {
	for _, r := range b.steps {
		if r.running {
			return true
		}
	}
	return false
}

func (m Model) goalBanner(b *goalBlock) []string {
	w := m.histColW - 12
	switch {
	case b.fatalErr != nil:
		return wrapStyled(styleDanger, "✗ error: "+b.fatalErr.Error(), w)
	case b.end == agentloop.EndDone:
		out := wrapStyled(styleSafe, "✔ "+b.summary, w)
		if b.judgeNote != "" {
			out = append(out, wrapStyled(styleCaution, "⚠ "+b.judgeNote, w)...)
		}
		return out
	case b.end == agentloop.EndDeclined:
		return []string{"  " + styleMuted.Render(fmt.Sprintf("✗ declined — %d command(s) ran", len(b.steps)))}
	case b.end == agentloop.EndAborted:
		return []string{"  " + styleCaution.Render(fmt.Sprintf("⚠ aborted — %d command(s) ran", len(b.steps)))}
	case b.end == agentloop.EndBudget:
		return []string{"  " + styleCaution.Render("⚠ step cap reached — goal not confirmed done")}
	default:
		return []string{"  " + styleMuted.Render("ended: "+string(b.end))}
	}
}

func previewLines(r *stepRow, width int) []string {
	src := r.live
	if r.ec != nil {
		src = strings.Split(strings.TrimSuffix(r.ec.Result.Stdout+r.ec.Result.Stderr, "\n"), "\n")
	}
	var out []string
	for i, l := range src {
		if i >= 3 {
			out = append(out, styleFaint.Render(fmt.Sprintf("    … +%d more (viewport below)", len(src)-3)))
			break
		}
		out = append(out, "    "+styleFaint.Render(truncateWidth(l, width)))
	}
	return out
}
