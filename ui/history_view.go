package ui

import (
	"fmt"
	"github.com/vitzeno/detent/event"
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
	step := 0 // counts across the whole session, not per goal
	for bi, b := range m.blocks {
		if bi > 0 {
			lines = append(lines, "")
		}
		var block []string

		for _, gl := range wrapPlain(b.prompt, m.blockWidth()) {
			block = append(block, styleGoal.Render(gl))
		}
		for _, r := range b.rows {
			if len(rows) > 0 && r == rows[m.cursorClamped()] {
				cursorEntry = len(lines) + len(block)
			}
			step++
			block = append(block, m.stepLines(r, step)...)
			if r.expanded {
				block = append(block, previewLines(r, m.blockWidth()-6)...)
			}
		}
		if b.ended {
			block = append(block, m.turnBanner(b)...)
		} else if b == m.cur && m.waiting && !anyRunning(b) {
			block = append(block, fmt.Sprintf("  %s %s", m.spinner.View(), styleFaint.Render("thinking…")))
		}
		lines = append(lines, m.railed(b, block)...)
	}
	if len(lines) == 0 {
		lines = append(lines, styleFaint.Render("  nothing yet — ask for something below"))
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
func (m Model) railed(b *turnBlock, block []string) []string {
	bar := m.railStyle(b).Render("┃") + " "
	out := make([]string, len(block))
	for i, l := range block {
		out[i] = bar + l
	}
	return out
}

// railStyle colours the rail by outcome: accent while the request is
// still running, then whatever it came to.
func (m Model) railStyle(b *turnBlock) lipgloss.Style {
	switch {
	case b == m.cur && !b.ended:
		return styleRowCursor
	case !b.ended:
		return styleRowCursor
	case b.err != "" || b.end == event.EndError:
		return styleDanger
	case b.end == event.EndDone:
		return styleSafe
	case b.end == event.EndStopped:
		return styleSafe
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

// step is this row's 1-based position in the session, what
// /rollback's argument targets, shown as a dim marker on any step
// that ran sandboxed. Session-wide, not per goal: numbering restarted
// each goal meant every row read "#1" and the number a human typed
// addressed a different step from the one they were pointing at.
func (m Model) stepLines(r *callRow, step int) []string {
	mark := "  "
	if r == m.focused() {
		mark = styleRowCursor.Render("▸ ")
	}
	// The model speaking, not a command: no status badge, because
	// nothing ran and there is no exit code to report.
	if r.prose != "" {
		note := ""
		if k := r.kind(); k != "" && k != "text" {
			note = styleFaint.Render("  " + status.KindLabel(k))
		}
		return []string{fmt.Sprintf("%s%s %s%s", mark, styleGoal.Render("❯"),
			styleMuted.Render(layout.Truncate(plainProse(r.prose), m.blockWidth()-10)), note)}
	}
	s := status.Row{}
	switch {
	case r.running:
		s.Running = true
		s.LiveLines = len(r.live)
		s.Dropped = r.dropped
	case r.result != nil:
		s.HasResult = true
		s.ExitCode = r.result.ExitCode
		s.Summary = resultSummary(r.result)
		if r.post != nil {
			s.Judged = true
			s.Status = r.post.status
			s.Attention = r.post.attention
		}
	}
	icon, detail := status.Badge(s, m.spinner.View())
	_ = step

	// Truncated not wrapped: a command is often one unbreakable token
	// with no good place to break. The budget is measured from the
	// pieces rather than guessed at, because the detail text varies
	// and a fixed allowance overflowed the pane by a column.
	// Measured from the pieces, since the detail varies. Where there
	// is no room for both, the command wins.
	head := lipgloss.Width(stripStyle(mark)) + lipgloss.Width(icon) + 1
	tail := "· " + detail
	if m.blockWidth()-head-1-lipgloss.Width(tail) < minCommandCells {
		cmd := layout.Truncate(r.command, m.blockWidth()-head)
		return []string{fmt.Sprintf("%s%s %s", mark, icon, cmd)}
	}
	cmd := layout.Truncate(r.command, m.blockWidth()-head-1-lipgloss.Width(tail))
	return []string{fmt.Sprintf("%s%s %s %s", mark, icon, cmd, styleMuted.Render(tail))}
}

// minCommandCells is what layout.Truncate will not go below.
const minCommandCells = 4

// stripStyle measures what a styled string occupies, since a row's
// budget is cells and ANSI is bytes.
func stripStyle(s string) string {
	out := make([]rune, 0, len(s))
	var inEsc bool
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc && (r == 'm' || r == 'K'):
			inEsc = false
		case !inEsc:
			out = append(out, r)
		}
	}
	return string(out)
}

func anyRunning(b *turnBlock) bool {
	for _, r := range b.rows {
		if r.running {
			return true
		}
	}
	return false
}

// turnBanner is the one line under a finished block. The model's own
// words have a row of their own, so this is the outcome alone.
func (m Model) turnBanner(b *turnBlock) []string {
	w := m.blockWidth() - 2
	ran := fmt.Sprintf("%d call(s)", len(b.rows))
	switch b.end {
	case event.EndDone:
		return nil
	case event.EndStopped:
		return []string{"  " + styleFaint.Render("✓ stopped early — "+ran)}
	case event.EndBound:
		return []string{"  " + styleCaution.Render("⚠ step bound reached — "+ran)}
	case event.EndAborted:
		return []string{"  " + styleCaution.Render("⚠ aborted — "+ran)}
	case event.EndError:
		return wrapStyled(styleDanger, "✗ error: "+b.err, w)
	}
	return []string{"  " + styleMuted.Render("ended: "+string(b.end))}
}

// resultSummary is the short form a row shows beside its badge.
func resultSummary(r *event.Result) string {
	if r.Err != "" {
		return r.Err
	}
	out := outputOf(r)
	if out == "" {
		return "no output"
	}
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	return out
}

func previewLines(r *callRow, width int) []string {
	src := r.live
	if r.result != nil {
		src = strings.Split(strings.TrimSuffix(outputOf(r.result), "\n"), "\n")
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

// plainProse drops the markup a one-line row cannot render, so a
// summary written as markdown reads as a sentence rather than as its
// own source. The pane still draws the real thing.
func plainProse(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "#-*> \t")
	return strings.NewReplacer("`", "", "**", "", "__", "").Replace(s)
}
