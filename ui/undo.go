package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/event"
)

// Undo asks one question first: the container goes back either way,
// but a request may have touched work detent never made.

// runUndo handles /undo [n], where n is a request number as history
// shows it. No argument means the last one that can be undone.
func (m Model) runUndo(input string) (tea.Model, tea.Cmd) {
	b, err := m.undoTarget(input)
	if err != "" {
		m.noteErr(err)
		return m, nil
	}
	m.undo.target = b
	m.mode = modeUndo
	m.nav.focus = focusOutput
	return m, nil
}

func (m Model) undoTarget(input string) (*turnBlock, string) {
	arg := strings.TrimSpace(strings.TrimPrefix(input, "/undo"))
	if arg == "" {
		for i := len(m.blocks) - 1; i >= 0; i-- {
			if m.blocks[i].undoable && m.blocks[i].ended {
				return m.blocks[i], ""
			}
		}
		return nil, "nothing to undo"
	}
	n, err := strconv.Atoi(arg)
	if err != nil {
		return nil, "usage: /undo [request number]"
	}
	for _, b := range m.blocks {
		if b.n != n {
			continue
		}
		switch {
		case !b.ended:
			return nil, fmt.Sprintf("request %d is still running", n)
		case !b.undoable:
			return nil, fmt.Sprintf("request %d was not checkpointed", n)
		}
		return b, ""
	}
	return nil, fmt.Sprintf("no request %d", n)
}

// confirmUndo publishes the intent. revertFiles is the answer to the
// only question worth asking: its default is the non-destructive one.
func (m Model) confirmUndo(revertFiles bool) (tea.Model, tea.Cmd) {
	b := m.undo.target
	m.undo.target = nil
	m.backToInput()
	if b == nil {
		return m, nil
	}
	return m, m.send(event.RequestRollback{Turn: b.id, RevertFiles: revertFiles})
}

func (m Model) cancelUndo() (tea.Model, tea.Cmd) {
	m.undo.target = nil
	m.backToInput()
	return m, nil
}

// undoLines shows what goes, in the pane rather than a modal: a list
// you cannot read to the end is not one you can approve.
func (m *Model) undoLines() []string {
	b := m.undo.target
	if b == nil {
		return nil
	}
	reversible, standing := split(b.rows)
	width := m.layout.outputColW - 6

	out := []string{styleCaution.Render(fmt.Sprintf("undo request #%d", b.n)), "",
		"  " + truncCell(b.prompt, width), "",
		styleFaint.Render(fmt.Sprintf("  %d call(s) will be undone", len(reversible))), ""}
	for _, r := range reversible {
		out = append(out, "  "+styleMuted.Render(truncCell(r.command, width)))
	}

	// Named, not counted: a rollback that quietly does less than a
	// human expects is the worst thing this page could do.
	if len(standing) > 0 {
		out = append(out, "",
			styleDanger.Render(fmt.Sprintf("  %d call(s) cannot be undone", len(standing))))
		for _, r := range standing {
			out = append(out, "  "+styleDanger.Render(truncCell(r.command, width)))
		}
	}
	return append(out, "",
		styleFaint.Render("  The container goes back either way."),
		styleFaint.Render("  Your own files only go back if you say so — this"),
		styleFaint.Render("  request may have touched work detent never made."))
}

// split separates what a checkpoint covers from what it does not.
// Prose is neither: nothing ran.
func split(rows []*callRow) (reversible, standing []*callRow) {
	for _, r := range rows {
		switch {
		case r.prose != "":
		case r.executor != "":
			standing = append(standing, r)
		default:
			reversible = append(reversible, r)
		}
	}
	return reversible, standing
}
