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

// The finder's box: hits on the left, the selected one drawn on the right.

// finderChrome is the box's lines that hold no hit: title, query, two
// rules and the key line.
const finderChrome = 5

func (m Model) finderBox() string {
	inner := island.Inner(m.modalWidth())
	rule := styleFaint.Render(strings.Repeat("─", inner))
	next := finderKindNames[(m.finder.kind+1)%finderKind(len(finderKindNames))]
	k := keymap.finder
	keys := boxLine(does("jump", k.jump), does(next, k.kind), does("move", k.move.up, k.move.down),
		does("scroll", k.move.pageUp, k.move.pageDown), does("back", k.close),
		does("first/last", k.move.top, k.move.bottom))

	lines := []string{m.finderQueryLine(inner), rule}
	lines = append(lines, m.finderBodyLines()...)
	return m.modalBox(m.finderTitle(), append(lines, rule), keys)
}

func (m Model) finderTitle() string {
	title := styleBrand.Render("finder") + styleFaint.Render(" · "+finderKindNames[m.finder.kind])
	// A question waits for the finder to close, so say one is there.
	if m.asking() != nil || m.bound != nil {
		title += styleCaution.Render(" · a question is waiting, esc to see it")
	}
	return title
}

func (m Model) finderQueryLine(width int) string {
	count := styleFaint.Render(fmt.Sprintf("%d found", len(m.finder.hits)))
	left := styleBrand.Render("> ") + styleGoal.Render(m.finder.query) + styleBrand.Render("▏")
	gap := width - lipgloss.Width(left) - lipgloss.Width(count)
	if gap < 1 {
		return ansi.Truncate(left, width, "")
	}
	return left + strings.Repeat(" ", gap) + count
}

// finderBodyLines sets the list and the preview side by side.
func (m Model) finderBodyLines() []string {
	height, listW, prevW := m.finderBodyHeight(), m.finderListWidth(), m.finderPreviewWidth()
	list := m.finderListLines(listW, height)
	var preview []string
	if h, ok := m.finderSelected(); ok {
		lines, match := m.finderPreview(h, prevW-1)
		top := previewStart(len(lines), match, height, m.finder.scroll)
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

// finderListLines is the window of hits that keeps the cursor in view.
func (m Model) finderListLines(width, height int) []string {
	if len(m.finder.hits) == 0 {
		msg := "nothing matches"
		if len(m.rows()) == 0 {
			msg = "nothing in history yet"
		}
		return []string{styleFaint.Render("  " + msg)}
	}
	top, end := listWindow(len(m.finder.hits), m.finder.cursor, height)
	var out []string
	for i := top; i < end; i++ {
		h := m.finder.hits[i]
		mark := "  "
		if i == m.finder.cursor {
			mark = styleRowCursor.Render("▸ ")
		}
		glyph := m.finderGlyph(h)
		used := 2 + lipgloss.Width(glyph) + 1
		out = append(out, mark+glyph+" "+highlight(h.label, h.pos, width-used))
	}
	return out
}

// finderGlyph says what matched: a request's number, a command, or a line
// of output.
func (m Model) finderGlyph(h finderHit) string {
	switch {
	case h.kind == finderPrompts:
		return styleGoal.Render(fmt.Sprintf("#%d", h.block.n))
	case h.kind == finderOutput && h.row.prose != "":
		return styleGoal.Render("❯")
	case h.kind == finderOutput:
		return styleFaint.Render("↳")
	case h.row.human:
		return styleGoal.Render(m.prompt.mark())
	}
	return styleFaint.Render("›")
}

// finderPreview is the selected hit drawn as the output pane would, and
// the line to centre on.
func (m Model) finderPreview(h finderHit, width int) (lines []string, match int) {
	if h.kind == finderPrompts {
		return m.requestPreview(h.block, width), 0
	}
	switch r := h.row; {
	case r.signin != nil:
		lines = signInPageLines(r.signin, width)
	case r.running:
		lines = r.live
	default:
		rendered, ok := m.drawRow(r, width, m.finderBodyHeight(), false)
		if !ok {
			return []string{styleFaint.Render("(no output)")}, 0
		}
		lines = rendered.Lines
	}
	if line, _, ok := search.Lines(m.finder.query, ansi.Strip(strings.Join(lines, "\n"))); ok {
		return lines, line
	}
	return lines, 0
}

// requestPreview is a request's prompt, then a line per row it made.
func (m Model) requestPreview(b *turnBlock, width int) []string {
	lines := wrapPlain(oneLine(strings.TrimSpace(b.prompt)), width)
	for i := range lines {
		lines[i] = styleGoal.Render(lines[i])
	}
	lines = append(lines, "")
	for _, r := range b.rows {
		text := r.command
		if r.prose != "" {
			text = "❯ " + plainProse(r.prose)
		}
		lines = append(lines, styleFaint.Render(layout.Truncate(text, width)))
	}
	return lines
}

func (m Model) finderBodyHeight() int { return max(1, m.modalHeight()-2-finderChrome) }
func (m Model) finderListWidth() int  { return island.Inner(m.modalWidth()) * 2 / 5 }

func (m Model) finderPreviewWidth() int {
	return max(1, island.Inner(m.modalWidth())-m.finderListWidth()-3)
}

// previewStart puts the match a third of the way down, moved by scroll,
// and never past either end.
func previewStart(n, match, height, scroll int) int {
	top := match - height/3 + scroll
	return min(max(top, 0), max(0, n-height))
}

// highlight fits label to width cells with the runes at pos picked out,
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
	// Runs of matched or unmatched runes are styled whole, not a rune at a time.
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
