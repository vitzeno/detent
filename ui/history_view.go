package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/layout"
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
			lines = append(lines, "")
		}
		var block []string

		// A tool block has no goal text of its own — its one step
		// already says what it is.
		if b.tool == "" {
			for _, gl := range wrapPlain(b.goal, m.blockWidth()) {
				block = append(block, styleGoal.Render(gl))
			}
		}
		for i, r := range b.steps {
			if len(rows) > 0 && r == rows[m.cursorClamped()] {
				cursorEntry = len(lines) + len(block)
			}
			block = append(block, m.stepLines(r, i+1)...)
			if r.cmd.expanded {
				block = append(block, previewLines(r, m.blockWidth()-6)...)
			}
		}
		if b.ended && b.tool == "" {
			block = append(block, m.goalBanner(b)...)
		} else if b == m.cur && m.waiting && !anyRunning(b) {
			block = append(block, fmt.Sprintf("  %s %s", m.spinner.View(), styleFaint.Render("thinking…")))
		}
		lines = append(lines, m.railed(b, block)...)
	}
	if len(lines) == 0 {
		lines = append(lines, styleFaint.Render("  no goals yet — describe one below"))
	}
	return lines, cursorEntry
}

// railWidth is the gutter the rail and its space occupy.
const railWidth = 2

// blockWidth is what a block's own content gets, once the island's
// padding and the rail gutter have taken theirs.
func (m Model) blockWidth() int { return max(12, m.layout.histColW-4-railWidth) }

// railed draws a block's rows against a coloured bar spanning all of
// them. A divider only marks where blocks meet; a rail marks how far
// one reaches, so the grouping reads from any row rather than only
// from the edges — and its colour says how the goal went without
// having to reach the banner at the bottom.
func (m Model) railed(b *goalBlock, block []string) []string {
	bar := m.railStyle(b).Render("┃") + " "
	out := make([]string, len(block))
	for i, l := range block {
		out[i] = bar + l
	}
	return out
}

// railStyle colours the rail by outcome: accent while the goal is
// still running, then whatever it came to.
func (m Model) railStyle(b *goalBlock) lipgloss.Style {
	switch {
	case b == m.cur && !b.ended:
		return styleRowCursor
	case b.tool != "":
		return styleFaint
	case b.fatalErr != nil:
		return styleDanger
	case b.end == EndDone && b.judge.scored && b.judge.score < partlyMetBelow:
		return styleCaution
	case b.end == EndDone:
		return styleSafe
	case b.end == EndDeclined:
		return styleMuted
	case !b.ended:
		return styleRowCursor
	default:
		return styleCaution
	}
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
		cmd := layout.Truncate(r.command, m.blockWidth()-4)
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
	cmd := layout.Truncate(r.command, m.blockWidth()-24)
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
	w := m.blockWidth() - 2
	switch {
	case b.fatalErr != nil:
		return wrapStyled(styleDanger, "✗ error: "+b.fatalErr.Error(), w)
	case b.end == EndDone:
		// Just the model's prose, dimmed and unmarked: the step rows
		// above already carry the status and the jev line below the
		// verdict, so a tick here is a third signal saying neither.
		return append(wrapStyled(styleMuted, b.summary, w), m.judgeLines(b, w)...)
	case b.end == EndBudget:
		return append([]string{"  " + styleCaution.Render("⚠ step cap reached — goal not confirmed done")},
			m.judgeLines(b, w)...)
	case b.end == EndDeclined:
		return []string{"  " + styleMuted.Render(fmt.Sprintf("✗ declined — %d command(s) ran", len(b.steps)))}
	case b.end == EndAborted:
		return []string{"  " + styleCaution.Render(fmt.Sprintf("⚠ aborted — %d command(s) ran", len(b.steps)))}
	default:
		return []string{"  " + styleMuted.Render("ended: "+string(b.end))}
	}
}

// judgeLines reports Jev's read on the goal whatever it says. Speaking
// up only to disagree made silence mean both "it agrees" and "nothing
// judged this" — the two things a second opinion exists to separate.
func (m Model) judgeLines(b *goalBlock, width int) []string {
	if !b.judge.scored {
		return nil
	}
	// Only a verdict worth acting on gets a mark. Agreement is the
	// expected case, and flagging it crowds out the two that aren't.
	mark, style, word := "", styleSafe, "goal met"
	switch {
	case b.judge.score < unmetBelow:
		mark, style, word = "⚠ ", styleCaution, "goal looks unmet"
	case b.judge.score < partlyMetBelow:
		mark, style, word = "~ ", styleCaution, "goal only partly met"
	}
	return wrapStyled(style, fmt.Sprintf("%sjev · %s (%.2f)", mark, word, b.judge.score), width)
}

// Where Jev's goal-achieved score stops meaning "done".
const (
	unmetBelow     = 0.5
	partlyMetBelow = 0.8
)

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
		out = append(out, "    "+styleFaint.Render(layout.Truncate(l, width)))
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
