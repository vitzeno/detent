package ui

import (
	"fmt"
	"strings"

	"github.com/vitzeno/detent/ui/welcome"
	"github.com/vitzeno/detent/version"
	"github.com/vitzeno/detent/viewspec"
)

// The output pane: what the focused row's component renders into
// it, and the viewport refresh that keeps it in step.

// detailLines renders the focused row: a pending question first,
// else the scrolling viewport.
func (m Model) detailLines() []string {
	// The undo list is what is being reviewed and can run to hundreds
	// of paths; the box below stays small and asks.
	if m.mode == modeUndo {
		return strings.Split(m.output.View(), "\n")
	}
	// A panel is a page about the session, drawn through the viewport
	// so it scrolls however long it runs.
	if m.panel.open != panelNone {
		return strings.Split(m.output.View(), "\n")
	}
	if m.showWelcome() {
		return m.welcomePane()
	}
	return strings.Split(m.output.View(), "\n")
}

// viewBody draws the row's view whole. Height says how tall the pane
// is so a plot can grow into it; nothing clips to it, so the viewport
// still windows the result and a view scrolls like any other output.
func (m Model) viewBody(r *callRow) (viewspec.Render, bool) {
	b, ok := boundView(r)
	if !ok {
		return viewspec.Render{}, false
	}
	out, err := b.Draw(viewspec.Frame{
		Width:   paneInner(m.layout.outputColW),
		Height:  m.output.Height(),
		Focused: m.nav.focus == focusOutput,
		Cursor:  r.tableCursor,
		Paint:   painter{},
	})
	if err != nil {
		return viewspec.Render{}, false
	}
	return out, true
}

// scrollMargin is how many lines of context the selection keeps. Two,
// so reaching a table's first row brings its header back rather than
// resting exactly on the top edge with the header just above it.
const scrollMargin = 2

// scrollToLine brings line into view without recentring: a selection
// already on screen must not make the pane jump under the cursor.
func (m *Model) scrollToLine(line int) {
	top, h := m.output.YOffset(), m.output.Height()
	if h <= 0 {
		return
	}
	switch {
	case line-scrollMargin < top:
		m.output.SetYOffset(max(0, line-scrollMargin))
	case line+scrollMargin >= top+h:
		m.output.SetYOffset(line + scrollMargin - h + 1)
	}
}

// helpLines lists every slash command via Match("/") (empty suffix
// matches all), so there's no separate list to keep in sync.
func helpLines() []string {
	var lines []string
	for _, c := range matchSlash("/") {
		lines = append(lines, fmt.Sprintf("  %-10s %s", c.Name, c.Desc))
	}
	return lines
}

func (m *Model) refreshViewport() {
	window, offset := m.historyWindow()
	m.nav.histWindow = window
	// -1 means following, which never counted a total to offset into.
	if offset >= 0 {
		m.nav.histOffset = offset
	}
	if m.panel.open != panelNone {
		m.setViewContent(strings.Join(m.panelLines(), "\n"))
		return
	}
	if m.mode == modeUndo {
		m.setViewContent(strings.Join(m.undoLines(), "\n"))
		return
	}
	r := m.focused()
	if r == nil {
		m.setViewContent(styleFaint.Render("(no output yet)"))
		return
	}
	var body string
	cursor := -1
	switch {
	case r.running:
		body = strings.Join(r.live, "\n")
	case r.drawable():
		rendered, ok := m.viewBody(r)
		if !ok {
			break
		}
		body, cursor = strings.Join(rendered.Lines, "\n"), rendered.CursorLine
	}
	if body == "" {
		m.setViewContent(styleFaint.Render("(no output)"))
		return
	}
	m.setViewContent(body)
	if r.running {
		m.output.GotoBottom()
	}
	if cursor >= 0 {
		m.scrollToLine(cursor)
	}
}

// welcomePane hands the boot pane the facts it reports, so nothing in
// ui/welcome reaches into Model.
func (m Model) welcomePane() []string {
	return welcome.Lines(welcome.Facts{
		Version:  version.String(),
		Proposer: m.run.Model, Judge: m.run.Judge, RunMode: m.runMode(),
		Image: m.info.Image, Mount: m.info.Mount,
		Runtime: m.info.Runtime, Network: m.info.Network,
		Goals: len(m.blocks), Commands: m.calls, Sessions: len(m.sessions),
		Session: m.run.Session.String(), Resumed: m.run.Resumed, Recorded: m.run.Recorded,
	}, paneInner(m.layout.outputColW), m.output.Height(), m.welcomeFrame)
}

// showWelcome reports whether the output pane has nothing of its own
// to show yet, which is the boot state: no row has ever been focused.
func (m Model) showWelcome() bool {
	return m.focused() == nil
}

// setViewContent skips identical content: SetContent resets scroll
// position, which must not happen just from resizing for the dropdown.
func (m *Model) setViewContent(s string) {
	if s == m.viewContent {
		return
	}
	m.viewContent = s
	m.output.SetContent(s)
}
