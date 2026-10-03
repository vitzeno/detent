package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vitzeno/detent/ui/island"
	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/search"
)

// The finder's box: hits on the left, the selected one drawn on the right,
// laid over the panes so the bars above and below stay where they were.

// findChrome is the box's lines that are not hits: title, query, two
// rules and the key line, inside the island's border.
const findChrome = 5

// withFinder lays the box over base while the finder is up.
func (m Model) withFinder(base string) string {
	if m.mode != modeFind {
		return base
	}
	box := m.findBox()
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(box).Y(1).Z(1),
	).Render()
}

// The box covers the panes exactly.
func (m Model) findWidth() int      { return max(minPaneWidth, m.layout.width) }
func (m Model) findHeight() int     { return m.nav.histHeight + islandOverhead }
func (m Model) findBodyHeight() int { return max(1, m.findHeight()-2-findChrome) }
func (m Model) findListWidth() int  { return island.Inner(m.findWidth()) * 2 / 5 }
func (m Model) findPreviewWidth() int {
	return max(1, island.Inner(m.findWidth())-m.findListWidth()-3)
}

func (m Model) findBox() string {
	inner := island.Inner(m.findWidth())
	rule := styleFaint.Render(strings.Repeat("─", inner))
	lines := []string{m.findQueryLine(inner), rule}
	lines = append(lines, m.findBodyLines()...)
	lines = append(lines, rule, styleFaint.Render(layout.Truncate(
		"enter jump · ctrl+r "+findKindNames[(m.find.kind+1)%findKind(len(findKindNames))]+
			" · ↑↓ move · ctrl+u/d scroll · esc back", inner)))
	return island.Render(m.findTitle(), palette.Accent, lines, m.findWidth(), m.findHeight()-2)
}

func (m Model) findTitle() string {
	title := styleBrand.Render("find") + styleFaint.Render(" · "+findKindNames[m.find.kind])
	// An approval waits for the finder to close, so say one is there.
	if m.asking != nil || m.bound != nil {
		title += styleCaution.Render(" · a question is waiting, esc to answer it")
	}
	return title
}

func (m Model) findQueryLine(width int) string {
	count := styleFaint.Render(fmt.Sprintf("%d found", len(m.find.hits)))
	left := styleBrand.Render("> ") + styleGoal.Render(m.find.query) + styleBrand.Render("▏")
	gap := width - lipgloss.Width(left) - lipgloss.Width(count)
	if gap < 1 {
		return ansi.Truncate(left, width, "")
	}
	return left + strings.Repeat(" ", gap) + count
}

// findBodyLines is the list beside the preview, one row of each per line.
func (m Model) findBodyLines() []string {
	height, listW, prevW := m.findBodyHeight(), m.findListWidth(), m.findPreviewWidth()
	list := m.findListLines(listW, height)
	var preview []string
	if h, ok := m.findSelected(); ok {
		lines, match := m.findPreview(h, prevW-1)
		top := previewStart(len(lines), match, height, m.find.scroll)
		for i := top; i < min(top+height, len(lines)); i++ {
			gutter := " "
			if i == match {
				gutter = styleBrand.Render("▌")
			}
			preview = append(preview, gutter+ansi.Truncate(lines[i], prevW-1, "…"))
		}
	}
	sep := styleFaint.Render(" │ ")
	out := make([]string, height)
	for i := range out {
		l, p := "", ""
		if i < len(list) {
			l = list[i]
		}
		if i < len(preview) {
			p = preview[i]
		}
		out[i] = l + strings.Repeat(" ", max(0, listW-lipgloss.Width(l))) + sep + p
	}
	return out
}

// findListLines is the window of hits that keeps the cursor on screen.
func (m Model) findListLines(width, height int) []string {
	if len(m.find.hits) == 0 {
		msg := "nothing matches"
		if len(m.rows()) == 0 {
			msg = "nothing in history yet"
		}
		return []string{styleFaint.Render("  " + msg)}
	}
	top := max(0, m.find.cursor-height+1)
	var out []string
	for i := top; i < min(top+height, len(m.find.hits)); i++ {
		h := m.find.hits[i]
		mark := "  "
		if i == m.find.cursor {
			mark = styleRowCursor.Render("▸ ")
		}
		glyph := m.findGlyph(h)
		used := 2 + lipgloss.Width(glyph) + 1
		out = append(out, mark+glyph+" "+highlight(h.label, h.pos, width-used))
	}
	return out
}

// findGlyph says what matched: a request by its number, a command, or a
// line of what something printed.
func (m Model) findGlyph(h findHit) string {
	switch {
	case h.kind == findPrompts:
		return styleGoal.Render(fmt.Sprintf("#%d", h.block.n))
	case h.kind == findOutput && h.row.prose != "":
		return styleGoal.Render("❯")
	case h.kind == findOutput:
		return styleFaint.Render("↳")
	case h.row.human:
		return styleGoal.Render(m.prompt.mark())
	}
	return styleFaint.Render("›")
}

// findPreview is what the selected hit shows on the right, and the line
// to centre on.
func (m Model) findPreview(h findHit, width int) (lines []string, match int) {
	if h.kind == findPrompts {
		lines = wrapPlain(oneLine(h.block.prompt), width)
		for i := range lines {
			lines[i] = styleGoal.Render(lines[i])
		}
		lines = append(lines, "")
		for _, r := range h.block.rows {
			text := r.command
			if r.prose != "" {
				text = "❯ " + plainProse(r.prose)
			}
			lines = append(lines, styleFaint.Render(layout.Truncate(text, width)))
		}
		return lines, 0
	}
	switch r := h.row; {
	case r.signin != nil:
		lines = signInPageLines(r.signin, width)
	case r.running:
		lines = r.live
	default:
		rendered, ok := m.drawRow(r, width, m.findBodyHeight(), false)
		if !ok {
			return []string{styleFaint.Render("(no output)")}, 0
		}
		lines = rendered.Lines
	}
	if line, _, ok := search.Lines(m.find.query, ansi.Strip(strings.Join(lines, "\n"))); ok {
		return lines, line
	}
	return lines, 0
}

// previewStart puts the match a third of the way down, moved by scroll,
// and never past either end.
func previewStart(n, match, height, scroll int) int {
	top := match - height/3 + scroll
	return min(max(top, 0), max(0, n-height))
}

// highlight draws label in width cells with the runes at pos picked out,
// sliding right when the match would fall past the edge.
func highlight(label string, pos []int, width int) string {
	runes := []rune(label)
	start := 0
	if n := len(pos); n > 0 && ansi.StringWidth(string(runes[:pos[n-1]+1])) > width-1 {
		start = max(0, pos[0]-4)
	}
	matched := make(map[int]bool, len(pos))
	for _, p := range pos {
		matched[p] = true
	}
	var b strings.Builder
	used := 0
	if start > 0 {
		b.WriteString(styleFaint.Render("…"))
		used++
	}
	var run []rune
	runHit := false
	flush := func() {
		if len(run) == 0 {
			return
		}
		style := styleMuted
		if runHit {
			style = styleBrand
		}
		b.WriteString(style.Render(string(run)))
		run = run[:0]
	}
	for i := start; i < len(runes); i++ {
		w := ansi.StringWidth(string(runes[i]))
		if used+w > width-1 && i < len(runes)-1 || used+w > width {
			flush()
			b.WriteString(styleFaint.Render("…"))
			break
		}
		if matched[i] != runHit {
			flush()
			runHit = matched[i]
		}
		run = append(run, runes[i])
		used += w
	}
	flush()
	return b.String()
}
