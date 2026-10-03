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

// findKind is what the finder searches, cycled by pressing ctrl+r again.
type findKind int

const (
	findAll findKind = iota
	findPrompts
	findCommands
	findOutput
)

var findKindNames = [...]string{"all", "prompts", "commands", "output"}

func (k findKind) wants(of findKind) bool { return k == findAll || k == of }

// findHit is one line of the finder's list.
type findHit struct {
	kind findKind
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

// openFind starts the finder over the panes. Only from the input state, so
// a key meant for a question can never open it.
func (m Model) openFind(query string) (Model, tea.Cmd) {
	if m.mode != modeInput {
		return m, nil
	}
	m.closePanel()
	m.find = findState{query: oneLine(query), saved: m.nav}
	m.mode = modeFind
	m.prompt.Blur()
	m.refind()
	return m, nil
}

// findKey owns every key while the finder is up.
func (m Model) findKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.closeFind(true), nil
	case "enter":
		return m.jump(), nil
	case "ctrl+r":
		m.find.kind = (m.find.kind + 1) % findKind(len(findKindNames))
		m.refind()
	case "up", "ctrl+p":
		m.moveFind(-1)
	case "down", "ctrl+n":
		m.moveFind(1)
	case "pgup", "ctrl+u":
		m.scrollFind(-m.findBodyHeight() / 2)
	case "pgdown", "ctrl+d":
		m.scrollFind(m.findBodyHeight() / 2)
	case "backspace":
		if _, n := utf8.DecodeLastRuneInString(m.find.query); n > 0 {
			m.find.query = m.find.query[:len(m.find.query)-n]
			m.refind()
		}
	case "ctrl+w":
		q := strings.TrimRight(m.find.query, " ")
		m.find.query = q[:strings.LastIndex(q, " ")+1]
		m.refind()
	default:
		if msg.Text != "" {
			m.find.query += oneLine(msg.Text)
			m.refind()
		}
	}
	return m, nil
}

// closeFind puts the bar back, and with restore the cursor too. A question
// that arrived meanwhile is raised now, since backToInput raises it.
func (m Model) closeFind(restore bool) Model {
	saved := m.find.saved
	m.find = findState{}
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
	if m.find.cursor >= len(m.find.hits) {
		return m.closeFind(true)
	}
	h, query := m.find.hits[m.find.cursor], m.find.query
	m = m.closeFind(false)
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
	if h.kind == findOutput {
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

func (m *Model) refind() {
	m.find.hits = m.findHits(m.find.query, m.find.kind)
	m.find.cursor, m.find.scroll = 0, 0
}

func (m *Model) moveFind(d int) {
	m.find.cursor = min(max(m.find.cursor+d, 0), max(0, len(m.find.hits)-1))
	m.find.scroll = 0
}

// scrollFind moves the preview, held to what the preview has to show.
func (m *Model) scrollFind(d int) {
	h, ok := m.findSelected()
	if !ok {
		return
	}
	lines, match := m.findPreview(h, m.findPreviewWidth())
	height := m.findBodyHeight()
	auto := previewStart(len(lines), match, height, 0)
	top := min(max(auto+m.find.scroll+d, 0), max(0, len(lines)-height))
	m.find.scroll = top - auto
}

func (m Model) findSelected() (findHit, bool) {
	if m.find.cursor >= len(m.find.hits) {
		return findHit{}, false
	}
	return m.find.hits[m.find.cursor], true
}

// findHits matches query against every block, best first. Prompts and
// commands are short, so fuzzy. Output is long, so a literal line.
func (m Model) findHits(query string, kind findKind) []findHit {
	var hits []findHit
	order := 0
	for _, b := range m.blocks {
		// A block with no rows has nowhere for the cursor to land.
		if len(b.rows) == 0 {
			continue
		}
		if b.prompt != "" && kind.wants(findPrompts) {
			label := oneLine(b.prompt)
			if h, ok := search.Fuzzy(query, label); ok {
				hits = append(hits, findHit{kind: findPrompts, row: b.rows[0], block: b,
					label: label, pos: h.Pos, score: h.Score, order: order})
			}
		}
		for _, r := range b.rows {
			if r.command != "" && kind.wants(findCommands) {
				label := oneLine(r.command)
				if h, ok := search.Fuzzy(query, label); ok {
					hits = append(hits, findHit{kind: findCommands, row: r, block: b,
						label: label, pos: h.Pos, score: h.Score, order: order})
				}
			}
			if query != "" && kind.wants(findOutput) {
				if hit, ok := outputHit(query, r); ok {
					hit.block, hit.order = b, order
					hits = append(hits, hit)
				}
			}
			order++
		}
	}
	slices.SortStableFunc(hits, func(a, b findHit) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(b.order, a.order),
			cmp.Compare(a.kind, b.kind))
	})
	return hits
}

// outputHit is the first line of a row's output, or the model's words,
// holding every term. One per row, so a log repeating a word buries nothing.
func outputHit(query string, r *historyRow) (findHit, bool) {
	text := r.searchable()
	line, h, ok := text.Lines(query)
	if !ok {
		return findHit{}, false
	}
	raw := text.Line(line)
	label := strings.TrimLeft(raw, " ")
	shift := utf8.RuneCountInString(raw) - utf8.RuneCountInString(label)
	pos := make([]int, len(h.Pos))
	for i, p := range h.Pos {
		pos[i] = p - shift
	}
	return findHit{kind: findOutput, row: r, label: label, pos: pos, score: h.Score}, true
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
