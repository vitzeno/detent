package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/termsafe"
)

// The review modal: what one request changed in the human's files, read
// between the checkpoints taken as it began and as it ended.

// diffRow is one line the diff pane draws, a hunk's header when line is -1.
type diffRow struct{ hunk, line int }

// showReview is /review: the last request that checkpointed the files, or request N.
func (m Model) showReview(input string) (Model, tea.Cmd) {
	b, why := m.reviewTarget(strings.TrimSpace(strings.TrimPrefix(input, "/review")))
	if b == nil {
		m.noteErr(why)
		return m, nil
	}
	m.review = reviewState{block: b, base: b.base, head: m.reviewHead(b), loading: true, back: m.nav.focus}
	m.mode = modeReview
	return m, m.send(event.LoadDiff{Base: m.review.base, Head: m.review.head})
}

// reviewTarget is the request /review means, or why there is none.
func (m Model) reviewTarget(arg string) (*turnBlock, string) {
	if arg == "" {
		for _, b := range slices.Backward(m.blocks) {
			if b.base != "" {
				return b, ""
			}
		}
		return nil, "nothing to review: no request has checkpointed your files, which needs a git work tree"
	}
	n, err := strconv.Atoi(strings.TrimPrefix(arg, "#"))
	if err != nil {
		return nil, "/review takes a request's number, e.g. /review 3"
	}
	for _, b := range m.blocks {
		if b.n != n || b.userCommands || b.seam != nil {
			continue
		}
		if b.base == "" {
			return nil, fmt.Sprintf("request %d has no checkpoint of your files to review", n)
		}
		return b, ""
	}
	return nil, fmt.Sprintf("there is no request %d", n)
}

// reviewHead is where the request left the files: its own end, else for one
// stored before that was recorded the next request's start, else the files now.
func (m Model) reviewHead(b *turnBlock) string {
	if b.tree != "" || !b.ended {
		return b.tree
	}
	for _, next := range m.blocks[slices.Index(m.blocks, b)+1:] {
		if next.base != "" {
			return next.base
		}
	}
	return ""
}

// diffLoaded fills the review it answers, defusing what the files say once, here.
func (m *Model) diffLoaded(v event.DiffLoaded) {
	r := &m.review
	if !r.loading || v.Base != r.base || v.Head != r.head {
		return
	}
	r.loading, r.cut, r.err = false, v.Cut, v.Err
	r.files = defused(v.Files)
}

// closeReview returns to the pane the review was opened from.
func (m Model) closeReview() (Model, tea.Cmd) {
	back := m.review.back
	m.review = reviewState{}
	m.backToInput()
	if m.mode == modeInput && back != focusInput {
		m.nav.focus = back
		m.prompt.Blur()
	}
	return m, nil
}

// reviewKey owns every key while the review is open.
func (m Model) reviewKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	r := &m.review
	switch msg.String() {
	case "esc":
		return m.closeReview()
	case "tab":
		r.diffFocused = !r.diffFocused
	case "enter":
		r.diffFocused = true
	case "up", "k":
		r.move(-1)
	case "down", "j":
		r.move(1)
	case "pgup":
		r.move(-m.modalPaneHeight())
	case "pgdown":
		r.move(m.modalPaneHeight())
	case "n":
		r.pickFile(r.file + 1)
	case "p":
		r.pickFile(r.file - 1)
	case "]":
		r.line = r.nextHunk(1)
	case "[":
		r.line = r.nextHunk(-1)
	}
	return m, nil
}

// move steps the line in the diff pane, or the file in the list.
func (r *reviewState) move(d int) {
	if !r.diffFocused {
		r.pickFile(r.file + d)
		return
	}
	r.line = min(max(r.line+d, 0), max(0, len(r.rows())-1))
}

// pickFile selects file i, from its first line.
func (r *reviewState) pickFile(i int) {
	r.file, r.line = min(max(i, 0), max(0, len(r.files)-1)), 0
}

// selected is the file under the cursor, nil while there is none.
func (r *reviewState) selected() *event.FileDiff {
	if r.file >= len(r.files) {
		return nil
	}
	return &r.files[r.file]
}

// rows is the selected file as the diff pane lists it, each hunk's header first.
func (r *reviewState) rows() []diffRow {
	f := r.selected()
	if f == nil {
		return nil
	}
	var out []diffRow
	for h, hunk := range f.Hunks {
		out = append(out, diffRow{hunk: h, line: -1})
		for l := range hunk.Lines {
			out = append(out, diffRow{hunk: h, line: l})
		}
	}
	return out
}

// nextHunk is the row of the next hunk's header in direction d, or the line
// unmoved when there is none that way.
func (r *reviewState) nextHunk(d int) int {
	rows := r.rows()
	for i := r.line + d; i >= 0 && i < len(rows); i += d {
		if rows[i].line < 0 {
			return i
		}
	}
	return r.line
}

// defused is files with every path and line made safe to print. A copy, since
// an event is shared, and a CRLF line loses its \r rather than showing it.
func defused(files []event.FileDiff) []event.FileDiff {
	out := make([]event.FileDiff, len(files))
	for i, f := range files {
		f.Path = termsafe.Printable(f.Path)
		f.Hunks = slices.Clone(f.Hunks)
		for j, h := range f.Hunks {
			h.Header = termsafe.Printable(h.Header)
			h.Lines = slices.Clone(h.Lines)
			for k, l := range h.Lines {
				h.Lines[k].Text = termsafe.Printable(strings.TrimSuffix(l.Text, "\r"))
			}
			f.Hunks[j] = h
		}
		out[i] = f
	}
	return out
}
