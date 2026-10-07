package ui

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vitzeno/detent/defuse"
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

// finderModal is the finder: what is typed, what it matched, and where the
// human was so esc can put them back.
type finderModal struct {
	query  string
	kind   finderKind
	hits   []finderHit
	cursor int
	// scroll moves the preview from where it centres on the match.
	scroll int
	saved  navState
}

// openFinder opens only from the input state, so a key meant for a
// question can never open it.
func (m Model) openFinder(query string) (Model, tea.Cmd) {
	if m.mode != modeInput {
		return m, nil
	}
	m.closePanel()
	f := &finderModal{query: oneLine(strings.TrimSpace(query)), saved: m.nav}
	m.openModal(f)
	f.refresh(m)
	return m, nil
}

func (f *finderModal) key(m *Model, msg tea.KeyPressMsg) tea.Cmd {
	k := keymap.finder
	switch {
	case key.Matches(msg, k.close):
		f.close(m, true)
	case key.Matches(msg, k.jump):
		f.jump(m)
	case key.Matches(msg, k.kind):
		f.kind = (f.kind + 1) % finderKind(len(finderKindNames))
		f.refresh(*m)
	case key.Matches(msg, k.move.up, k.move.down, k.move.top, k.move.bottom):
		d, _ := k.move.delta(msg, 0)
		f.move(d)
	case key.Matches(msg, k.move.pageUp, k.move.pageDown):
		d, _ := k.move.delta(msg, m.finderBodyHeight()/2)
		f.scrollBy(*m, d)
	case key.Matches(msg, k.erase):
		if _, n := utf8.DecodeLastRuneInString(f.query); n > 0 {
			f.query = f.query[:len(f.query)-n]
			f.refresh(*m)
		}
	case key.Matches(msg, k.eraseWord):
		q := strings.TrimRight(f.query, " ")
		f.query = q[:strings.LastIndex(q, " ")+1]
		f.refresh(*m)
	default:
		if msg.Text != "" {
			f.add(*m, msg.Text)
		}
	}
	return nil
}

// sync does nothing: the list holds still while the agent works, as history does.
func (f *finderModal) sync(*Model) {}

func (f *finderModal) hint(Model) string {
	k := keymap.finder
	return barLine(does("closes", k.close), does("jump", k.jump), does("kind", k.kind),
		does("move", k.move.up, k.move.down))
}

// add adds text to the query, typed or pasted, on one line.
func (f *finderModal) add(m Model, text string) {
	f.query += oneLine(text)
	f.refresh(m)
}

// jump closes on the hit's row with following off, so nothing new pulls
// the cursor away from what was found.
func (f *finderModal) jump(m *Model) {
	h, ok := f.selected()
	if !ok {
		f.close(m, true)
		return
	}
	f.close(m, false)
	i := slices.Index(m.rows(), h.row)
	if i < 0 {
		m.noteErr("that is no longer in history")
		return
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
		m.scrollOutputTo(f.query)
	}
}

// close puts the bar back, and with restore the cursor and the pane too. Any
// question that waited is raised by closeModal.
func (f *finderModal) close(m *Model, restore bool) {
	if !restore {
		m.closeModal(focusInput)
		return
	}
	m.closeModal(f.saved.focus)
	m.nav.cursor = min(f.saved.cursor, max(0, len(m.rows())-1))
	m.nav.follow, m.nav.histOffset = f.saved.follow, f.saved.histOffset
	if f.saved.follow {
		m.trackNewest()
	}
}

// refresh matches the query afresh, from the best hit.
func (f *finderModal) refresh(m Model) {
	f.hits = m.finderHits(f.query, f.kind)
	f.cursor, f.scroll = 0, 0
}

func (f *finderModal) move(d int) {
	f.cursor = min(max(f.cursor+d, 0), max(0, len(f.hits)-1))
	f.scroll = 0
}

// scrollBy moves the preview, held within what it has to show.
func (f *finderModal) scrollBy(m Model, d int) {
	h, ok := f.selected()
	if !ok {
		return
	}
	lines, match := f.preview(m, h, m.finderPreviewWidth())
	height := m.finderBodyHeight()
	auto := previewStart(len(lines), match, height, 0)
	top := min(max(auto+f.scroll+d, 0), max(0, len(lines)-height))
	f.scroll = top - auto
}

func (f *finderModal) selected() (finderHit, bool) {
	if f.cursor >= len(f.hits) {
		return finderHit{}, false
	}
	return f.hits[f.cursor], true
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
		return search.NewText(defuse.Text(strings.Join(r.live, "\n")))
	}
	if r.found == nil || r.foundOf != r.result {
		t := search.NewText(defuse.Text(r.text()))
		r.found, r.foundOf = &t, r.result
	}
	return *r.found
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
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(defuse.Text(s))
}
