package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/status"
)

// The history pane: one entry per goal block, each with its steps
// under it. Everything here is pure — View calls it on a throwaway
// copy of Model, so nothing may write back.

// historyLines renders every history entry and reports which entry the
// cursor is on, rather than writing that to shared state on the way
// past. Pure, so View can call it on its throwaway copy.
func (m Model) historyLines() (lines []string, cursorEntry int) {
	rows := m.rows()
	for bi, b := range m.blocks {
		if bi > 0 {
			lines = append(lines, m.divider())
		}
		// A tool block has no goal text of its own — its one step already
		// says what it is.
		if b.tool == "" {
			for i, gl := range wrapPlain(b.goal, m.layout.histColW-14) {
				if i == 0 {
					lines = append(lines, styleMuted.Render("goal · ")+styleGoal.Render(gl))
				} else {
					lines = append(lines, contPrefix+styleGoal.Render(gl))
				}
			}
		}
		for i, r := range b.steps {
			if len(rows) > 0 && r == rows[m.cursorClamped()] {
				cursorEntry = len(lines)
			}
			lines = append(lines, m.stepLines(r, i+1)...)
			if r.cmd.expanded {
				lines = append(lines, previewLines(r, m.layout.histColW-10)...)
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
	return lines, cursorEntry
}

func (m *Model) cursorClamped() int {
	rows := m.rows()
	if len(rows) == 0 {
		return -1
	}
	if m.nav.cursor < 0 {
		return 0
	}
	if m.nav.cursor >= len(rows) {
		return len(rows) - 1
	}
	return m.nav.cursor
}

// step is this row's 1-based position, what /rollback's argument
// targets, shown as a dim marker on any step that ran sandboxed.
func (m Model) stepLines(r *stepRow, step int) []string {
	mark := "  "
	if r == m.focused() {
		mark = styleRowCursor.Render("▸ ")
	}
	// A tool row never executed a shell command, so status.Badge (which
	// reads r.cmd.ec) doesn't apply.
	if r.toolKind != "" {
		cmd := truncateWidth(r.command, m.layout.histColW-14)
		return []string{fmt.Sprintf("%s%s %s", mark, styleMuted.Render("○"), cmd)}
	}
	s := status.Row{}
	if r.cmd.running {
		s.Running = true
		s.LiveLines = len(r.cmd.live)
		s.Dropped = r.cmd.dropped
	} else if r.cmd.ec != nil {
		s.HasResult = true
		s.ExitCode = r.cmd.ec.Result.ExitCode
		s.Summary = r.cmd.ec.Result.Summary()
		if r.cmd.ec.Post != nil {
			s.Judged = true
			s.Status = r.cmd.ec.Post.Status
			s.Attention = r.cmd.ec.Post.Attention
		}
	}
	icon, detail := status.Badge(s, m.spinner.View())

	checkpoint := ""
	if r.cmd.ec != nil && r.cmd.ec.SnapshotID != "" {
		checkpoint = styleFaint.Render(fmt.Sprintf(" #%d", step))
	}

	// Truncated not wrapped: a command is often one unbreakable token
	// with no good place to break.
	cmd := truncateWidth(r.command, m.layout.histColW-34)
	return []string{fmt.Sprintf("%s%s %s %s%s", mark, icon, cmd, styleMuted.Render("· "+detail), checkpoint)}
}

func anyRunning(b *goalBlock) bool {
	for _, r := range b.steps {
		if r.cmd.running {
			return true
		}
	}
	return false
}

func (m Model) goalBanner(b *goalBlock) []string {
	w := m.layout.histColW - 12
	switch {
	case b.fatalErr != nil:
		return wrapStyled(styleDanger, "✗ error: "+b.fatalErr.Error(), w)
	case b.end == EndDone:
		out := wrapStyled(styleSafe, "✔ "+b.summary, w)
		if b.judgeNote != "" {
			out = append(out, wrapStyled(styleCaution, "⚠ "+b.judgeNote, w)...)
		}
		return out
	case b.end == EndDeclined:
		return []string{"  " + styleMuted.Render(fmt.Sprintf("✗ declined — %d command(s) ran", len(b.steps)))}
	case b.end == EndAborted:
		return []string{"  " + styleCaution.Render(fmt.Sprintf("⚠ aborted — %d command(s) ran", len(b.steps)))}
	case b.end == EndBudget:
		return []string{"  " + styleCaution.Render("⚠ step cap reached — goal not confirmed done")}
	default:
		return []string{"  " + styleMuted.Render("ended: "+string(b.end))}
	}
}

func previewLines(r *stepRow, width int) []string {
	src := r.cmd.live
	if r.cmd.ec != nil {
		src = strings.Split(strings.TrimSuffix(r.cmd.ec.Result.Stdout+r.cmd.ec.Result.Stderr, "\n"), "\n")
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

// contPrefix indents a wrapped line's continuation so it reads as one
// paragraph under its label, not flush against the pane edge.
const contPrefix = "    "

// wrapPlain word-wraps unstyled text — history wraps rather than
// truncates, unlike the output pane, since a human may be looking for
// exactly the part that would get cut.
func wrapPlain(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	wrapped := lipgloss.NewStyle().Width(width).Render(s)
	lines := strings.Split(strings.TrimRight(wrapped, "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ") // undo Width()'s padding
	}
	return lines
}

// wrapStyled styles each physical line; the first gets prefix, every
// continuation gets contPrefix, so a multi-line banner indents like a
// wrapped goal or command.
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

func (m Model) divider() string {
	return "  " + styleFaint.Render(strings.Repeat("─", min(20, max(4, m.layout.histColW-8))))
}
