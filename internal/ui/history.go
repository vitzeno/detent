package ui

import (
	"fmt"
	"strings"

	"github.com/vitzeno/detent/internal/agentloop"
	"github.com/vitzeno/detent/internal/ui/status"
)

// Records the focused row's line for cursor windowing.
func (m *Model) historyLines() []string {
	var lines []string
	rows := m.rows()
	for _, b := range m.blocks {
		lines = append(lines, styleMuted.Render("goal · ")+styleGoal.Render(truncateWidth(b.goal, m.width-14)))
		for _, r := range b.steps {
			if r == rows[m.cursorClamped()] && len(rows) > 0 {
				m.cursorLine = len(lines)
			}
			lines = append(lines, m.stepLine(r))
			if r.expanded {
				lines = append(lines, previewLines(r, m.width-10)...)
			}
		}
		if b.ended {
			lines = append(lines, m.goalBanner(b))
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

// Provisional styling first, upgraded when judged post lands — both
// from the status component.
func (m Model) stepLine(r *stepRow) string {
	mark := "  "
	if r == m.focused() {
		mark = styleRowCursor.Render("▸ ")
	}
	s := status.Row{}
	if r.running {
		s.Running = true
		s.LiveLines = len(r.live)
		s.Dropped = r.dropped
	} else {
		if r.result != nil {
			s.HasResult = true
			s.ExitCode = r.result.ExitCode
			s.Summary = r.result.Summary()
		}
		if r.post != nil {
			s.Judged = true
			s.Status = r.post.Status
			s.Attention = r.post.Attention
		}
	}
	icon, detail := status.Badge(s, m.spinner.View())
	cmd := truncateWidth(r.command, m.width-34)
	return fmt.Sprintf("%s%s %s %s", mark, icon, cmd, styleMuted.Render("· "+detail))
}

func anyRunning(b *goalBlock) bool {
	for _, r := range b.steps {
		if r.running {
			return true
		}
	}
	return false
}

func (m Model) goalBanner(b *goalBlock) string {
	switch {
	case b.fatalErr != nil:
		return "  " + styleDanger.Render("✗ error: "+b.fatalErr.Error())
	case b.end == agentloop.EndDone:
		s := "  " + styleSafe.Render("✔ "+truncateWidth(b.summary, m.width-12))
		if b.judgeNote != "" {
			s += "\n  " + styleCaution.Render("⚠ "+b.judgeNote)
		}
		return s
	case b.end == agentloop.EndDeclined:
		return "  " + styleMuted.Render(fmt.Sprintf("✗ declined — %d command(s) ran", len(b.steps)))
	case b.end == agentloop.EndAborted:
		return "  " + styleCaution.Render(fmt.Sprintf("⚠ aborted — %d command(s) ran", len(b.steps)))
	case b.end == agentloop.EndBudget:
		return "  " + styleCaution.Render("⚠ step cap reached — goal not confirmed done")
	default:
		return "  " + styleMuted.Render("ended: "+string(b.end))
	}
}

func previewLines(r *stepRow, width int) []string {
	src := r.live
	if r.result != nil {
		src = strings.Split(strings.TrimSuffix(r.result.Stdout+r.result.Stderr, "\n"), "\n")
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
