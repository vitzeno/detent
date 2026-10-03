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

// The finder: ctrl+r over this session's history, then a jump to the hit.
// It reads blocks and moves nav, and publishes nothing.

// finderKind is what the finder searches, cycled by pressing ctrl+r again.
type finderKind int

const (
	finderAll finderKind = iota
	finderPrompts
	finderCommands
	finderOutput
)

var finderKindNames = [...]string{"all", "prompts", "commands", "output"}

func (k finderKind) wants(of finderKind) bool { return k == finderAll || k == of }

// finderHit is one line of the finder's list.
type finderHit struct {
	kind finderKind
	// row is where a jump lands, held by pointer so an undo can't retarget it.
	row   *historyRow
	block *turnBlock
	// label is the one line the list shows, defused, and pos its matched runes.
	label string
	pos   []int
	score int
	// order is the row's place in history, so a tie goes to the newer one.
	order int
}

// openFinder starts the finder over the panes. Only from the input state, so
// a key meant for a question can never open it.
func (m Model) openFinder(query string) (Model, tea.Cmd) {
	if m.mode != modeInput {
		return m, nil
	}
	m.closePanel()
	m.finder = finderState{query: oneLine(query), saved: m.nav}
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
	case "ctrl+r":
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

// closeFinder puts the bar back, and with restore the cursor too. A question
// that arrived meanwhile is raised now, since backToInput raises it.
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

// jump closes the finder on the hit's row, with following off so nothing
// new pulls the cursor away from what was looked for.
func (m Model) jump() Model {
	if m.finder.cursor >= len(m.finder.hits) {
		return m.closeFinder(true)
	}
	h, query := m.finder.hits[m.finder.cursor], m.finder.query
	m = m.closeFinder(false)
	i := slices.Index(m.rows(), h.row)
	if i < 0 {
		m.noteErr("that is no longer in history")
		return m
	}
	m.nav.cursor, m.nav.follow = i, false
	_, at := m.historyAll()
	m.nav.histOffset = max(0, at-m.nav.histHeight/3)
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

// scrollOutputTo brings the first line of the drawn output holding query
// into view. The drawn lines, since a view need not keep the output's.
func (m *Model) scrollOutputTo(query string) {
	if line, _, ok := search.Lines(query, ansi.Strip(m.viewContent)); ok {
		m.output.SetYOffset(max(0, line-scrollMargin))
	}
}

func (m *Model) refreshFinder() {
	m.finder.hits = m.finderHits(m.finder.query, m.finder.kind)
	m.finder.cursor, m.finder.scroll = 0, 0
}

func (m *Model) moveFinder(d int) {
	m.finder.cursor = min(max(m.finder.cursor+d, 0), max(0, len(m.finder.hits)-1))
	m.finder.scroll = 0
}

// scrollFinder moves the preview, held to what the preview has to show.
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

// finderHits matches query against every block, best first. Prompts and
// commands are short, so fuzzy. Output is long, so a literal line.
func (m Model) finderHits(query string, kind finderKind) []finderHit {
	var hits []finderHit
	order := 0
	for _, b := range m.blocks {
		// A block with no rows has nowhere for the cursor to land.
		if len(b.rows) == 0 {
			continue
		}
		if b.prompt != "" && kind.wants(finderPrompts) {
			label := oneLine(b.prompt)
			if h, ok := search.Fuzzy(query, label); ok {
				hits = append(hits, finderHit{kind: finderPrompts, row: b.rows[0], block: b,
					label: label, pos: h.Pos, score: h.Score, order: order})
			}
		}
		for _, r := range b.rows {
			if r.command != "" && kind.wants(finderCommands) {
				label := oneLine(r.command)
				if h, ok := search.Fuzzy(query, label); ok {
					hits = append(hits, finderHit{kind: finderCommands, row: r, block: b,
						label: label, pos: h.Pos, score: h.Score, order: order})
				}
			}
			if query != "" && kind.wants(finderOutput) {
				if hit, ok := outputHit(query, r); ok {
					hit.block, hit.order = b, order
					hits = append(hits, hit)
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

// outputHit is the first line of a row's output, or the model's words,
// holding every term. One per row, so a log repeating a word buries nothing.
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

// searchable is the row's output, or its live tail while it runs, defused
// and prepared. Kept once the row has finished, since it no longer changes.
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

// oneLine defuses s and folds it onto a line, a rune for a rune, so offsets
// found in it are offsets in what is drawn.
func oneLine(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(termsafe.Printable(strings.TrimSpace(s)))
}
