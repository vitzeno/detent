package ui

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vitzeno/detent/termsafe"
	"github.com/vitzeno/detent/ui/search"
)

// The finder: ctrl+f over this session's history, then a jump to a hit.
// It reads blocks and moves nav, and publishes nothing.

// finderKind is what the finder searches. ctrl+f again cycles it.
type finderKind int

const (
	finderAll finderKind = iota
	finderPrompts
	finderCommands
	finderOutput
)

var finderKindNames = [...]string{"all", "prompts", "commands", "output"}

// finderHit is one line of the finder's list.
type finderHit struct {
	kind finderKind
	// row is where a jump lands, a pointer so an undo can't retarget it.
	row   *historyRow
	block *turnBlock
	// label is the line shown, defused, and pos its matched runes.
	label string
	pos   []int
	score int
	// order is the row's place in history, so a tie goes to the newer.
	order int
}

// openFinder opens only from the input state, so a key meant for a
// question can never open it.
func (m Model) openFinder(query string) (Model, tea.Cmd) {
	if m.mode != modeInput {
		return m, nil
	}
	m.closePanel()
	m.finder = finderState{query: oneLine(strings.TrimSpace(query)), saved: m.nav}
	m.mode = modeFinder
	m.prompt.Blur()
	m.refreshFinder()
	return m, nil
}

// finderKey owns every key while the finder is up.
func (m Model) finderKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.closeFinder(true), nil
	case "enter":
		return m.jump(), nil
	case "ctrl+f":
		m.finder.kind = (m.finder.kind + 1) % finderKind(len(finderKindNames))
		m.refreshFinder()
	case "up", "ctrl+p":
		m.moveFinder(-1)
	case "down", "ctrl+n":
		m.moveFinder(1)
	case "pgup", "ctrl+u":
		m.scrollFinder(-m.finderBodyHeight() / 2)
	case "pgdown", "ctrl+d":
		m.scrollFinder(m.finderBodyHeight() / 2)
	case "backspace":
		if _, n := utf8.DecodeLastRuneInString(m.finder.query); n > 0 {
			m.finder.query = m.finder.query[:len(m.finder.query)-n]
			m.refreshFinder()
		}
	case "ctrl+w":
		q := strings.TrimRight(m.finder.query, " ")
		m.finder.query = q[:strings.LastIndex(q, " ")+1]
		m.refreshFinder()
	default:
		if msg.Text != "" {
			m.finder.query += oneLine(msg.Text)
			m.refreshFinder()
		}
	}
	return m, nil
}

// jump closes on the hit's row with following off, so nothing new pulls
// the cursor away from what was found.
func (m Model) jump() Model {
	h, ok := m.finderSelected()
	if !ok {
		return m.closeFinder(true)
	}
	query := m.finder.query
	m = m.closeFinder(false)
	i := slices.Index(m.rows(), h.row)
	if i < 0 {
		m.noteErr("that is no longer in history")
		return m
	}
	m.nav.cursor, m.nav.follow = i, false
	_, at := m.historyAll()
	m.nav.histOffset = max(0, at-m.histRows()/3)
	if m.mode == modeInput {
		m.nav.focus = focusHistory
		m.prompt.Blur()
	}
	if h.kind == finderOutput {
		m.sizeViewport()
		m.scrollOutputTo(query)
	}
	return m
}

// closeFinder puts the bar back, and with restore the cursor too. Any
// question that waited is raised by backToInput.
func (m Model) closeFinder(restore bool) Model {
	saved := m.finder.saved
	m.finder = finderState{}
	m.backToInput()
	if !restore {
		return m
	}
	m.nav.cursor = min(saved.cursor, max(0, len(m.rows())-1))
	m.nav.follow, m.nav.histOffset = saved.follow, saved.histOffset
	if saved.follow {
		m.trackNewest()
	}
	if m.mode == modeInput && saved.focus != focusInput {
		m.nav.focus = saved.focus
		m.prompt.Blur()
	}
	return m
}

// finderHits is every match, best first. Prompts and commands are short,
// so fuzzy. Output is long, so a literal line.
func (m Model) finderHits(query string, kind finderKind) []finderHit {
	var hits []finderHit
	fuzzy := func(k finderKind, text string, r *historyRow, b *turnBlock, order int) {
		label := oneLine(strings.TrimSpace(text))
		if h, ok := search.Fuzzy(query, label); ok {
			hits = append(hits, finderHit{kind: k, row: r, block: b,
				label: label, pos: h.Pos, score: h.Score, order: order})
		}
	}
	order := 0
	for _, b := range m.blocks {
		// A block with no rows has nowhere for the cursor to land.
		if len(b.rows) == 0 {
			continue
		}
		if b.prompt != "" && kind.wants(finderPrompts) {
			fuzzy(finderPrompts, b.prompt, b.rows[0], b, order)
		}
		for _, r := range b.rows {
			if r.command != "" && kind.wants(finderCommands) {
				fuzzy(finderCommands, r.command, r, b, order)
			}
			if query != "" && kind.wants(finderOutput) {
				if h, ok := outputHit(query, r); ok {
					h.block, h.order = b, order
					hits = append(hits, h)
				}
			}
			order++
		}
	}
	slices.SortStableFunc(hits, func(a, b finderHit) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(b.order, a.order),
			cmp.Compare(a.kind, b.kind))
	})
	return hits
}

// outputHit is the first line of a row's output holding every term. One
// per row, so a log repeating a word buries nothing.
func outputHit(query string, r *historyRow) (finderHit, bool) {
	text := r.searchable()
	line, h, ok := text.Lines(query)
	if !ok {
		return finderHit{}, false
	}
	raw := text.Line(line)
	label := strings.TrimLeft(raw, " ")
	shift := utf8.RuneCountInString(raw) - utf8.RuneCountInString(label)
	pos := make([]int, len(h.Pos))
	for i, p := range h.Pos {
		pos[i] = p - shift
	}
	return finderHit{kind: finderOutput, row: r, label: label, pos: pos, score: h.Score}, true
}

// searchable is the row's output, or its live tail, defused and prepared.
// Kept once the row has finished, since it no longer changes.
func (r *historyRow) searchable() search.Text {
	if r.running {
		return search.NewText(termsafe.Printable(strings.Join(r.live, "\n")))
	}
	if r.found == nil || r.foundOf != r.result {
		t := search.NewText(termsafe.Printable(r.text()))
		r.found, r.foundOf = &t, r.result
	}
	return *r.found
}

func (m *Model) refreshFinder() {
	m.finder.hits = m.finderHits(m.finder.query, m.finder.kind)
	m.finder.cursor, m.finder.scroll = 0, 0
}

func (m *Model) moveFinder(d int) {
	m.finder.cursor = min(max(m.finder.cursor+d, 0), max(0, len(m.finder.hits)-1))
	m.finder.scroll = 0
}

// scrollFinder moves the preview, held within what it has to show.
func (m *Model) scrollFinder(d int) {
	h, ok := m.finderSelected()
	if !ok {
		return
	}
	lines, match := m.finderPreview(h, m.finderPreviewWidth())
	height := m.finderBodyHeight()
	auto := previewStart(len(lines), match, height, 0)
	top := min(max(auto+m.finder.scroll+d, 0), max(0, len(lines)-height))
	m.finder.scroll = top - auto
}

func (m Model) finderSelected() (finderHit, bool) {
	if m.finder.cursor >= len(m.finder.hits) {
		return finderHit{}, false
	}
	return m.finder.hits[m.finder.cursor], true
}

// scrollOutputTo shows the first drawn line holding query. Drawn, since a
// view need not keep the output's lines.
func (m *Model) scrollOutputTo(query string) {
	if line, _, ok := search.Lines(query, ansi.Strip(m.viewContent)); ok {
		m.output.SetYOffset(max(0, line-scrollMargin))
	}
}

func (k finderKind) wants(of finderKind) bool { return k == finderAll || k == of }

// oneLine defuses s onto one line, rune for rune, so offsets in it are
// offsets in what is drawn. It trims nothing: a typed space is a space.
func oneLine(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(termsafe.Printable(s))
}
