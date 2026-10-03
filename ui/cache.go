package ui

import "slices"

// blockKey is everything a block's drawing depends on. Comparable, so
// a hit is one equality check. Miss it and the pane renders stale.
type blockKey struct {
	rev, width int
	focused    *historyRow // nil unless the cursor is in this block
	spinner    string      // only the live block ever draws one
}

// blockKey carries focused only when the cursor is inside, so a
// cursor moving elsewhere leaves this block's key alone.
func (m Model) blockKey(b *turnBlock, focused *historyRow) blockKey {
	k := blockKey{rev: b.rev, width: m.blockWidth()}
	if slices.Contains(b.rows, focused) {
		k.focused = focused
	}
	// Every running row draws a spinner too, not just the thinking
	// line, so the frame is part of the key whenever either shows.
	if !b.ended && (anyRunning(b) || (b == m.cur && m.waiting)) {
		k.spinner = m.spinner.View()
	}
	return k
}

type blockCache struct {
	key      blockKey
	lines    []string
	cursorAt int
}

// histKey is everything the assembled history depends on. A scroll
// only moves the window over it, so it is a hit.
type histKey struct {
	rev, width, cursor int
	spinner            string // only while a block is still live
}

type histCache struct {
	key        histKey
	lines      []string
	cursorLine int
}

// detailKey is everything the output pane's content depends on. set is
// always true when computed, so the first render is never skipped.
type detailKey struct {
	set           bool
	rev           int
	width, height int
	panel         panelKind
	mode          mode
	row           *historyRow
	tableCursor   int
	focused       bool
}

func (m Model) detailKey() detailKey {
	k := detailKey{
		set: true, rev: m.histRev,
		width: paneInner(m.layout.outputColW), height: m.output.Height(),
		panel: m.panel.open, mode: m.mode, row: m.focused(),
		focused: m.nav.focus == focusOutput,
	}
	if k.row != nil {
		k.tableCursor = k.row.tableCursor
	}
	return k
}
