package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/vitzeno/detent/ui/editor"
	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/welcome"
	"github.com/vitzeno/detent/viewspec"
)

// The output pane: what the focused row's component renders into
// it, and the viewport refresh that keeps it in step.

// detailLines renders the focused row's component: editor takes
// priority when present, then a table for tabular output, else the
// scrolling viewport.
func (m Model) detailLines() []string {
	// A pending rollback takes the pane: its file list is the thing
	// being reviewed, and it can run to hundreds of paths. The confirm
	// box below stays small and asks the question.
	if m.mode == modeRollbackConfirm {
		return strings.Split(m.output.View(), "\n")
	}
	if m.showWelcome() {
		return m.welcomePane()
	}
	if r := m.focused(); r != nil {
		if r.editor != nil {
			return m.editorLines(r.editor)
		}
		switch r.toolKind {
		case "tree":
			return r.tool.tree.View(paneInner(m.layout.outputColW), m.output.Height())
		case "usage":
			return m.usageLines(r)
		case "help":
			return helpLines()
		}
	}
	return strings.Split(m.output.View(), "\n")
}

// viewBody draws the row's view whole — no Height — so the viewport
// windows it and a generated view scrolls like any other output.
func (m Model) viewBody(r *stepRow) (viewspec.Render, bool) {
	b, ok := boundView(r)
	if !ok {
		return viewspec.Render{}, false
	}
	out, err := b.Draw(viewspec.Frame{
		Width:   paneInner(m.layout.outputColW),
		Focused: m.nav.focus == focusOutput,
		Cursor:  r.cmd.tableCursor,
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

func (m Model) editorLines(e *editor.Model) []string {
	if e.Err() != nil {
		return []string{styleDanger.Render(e.View())} // "could not open <path>: <err>"
	}
	e.Resize(paneInner(m.layout.outputColW), m.output.Height())
	lines := strings.Split(e.View(), "\n")
	if e.Truncated() {
		lines = append(lines, styleCaution.Render(fmt.Sprintf("… file truncated at %d bytes", e.MaxBytes())))
	}
	return lines
}

func (m *Model) refreshViewport() {
	t0 := time.Now()
	defer func() {
		m.perf.uiPrep += time.Since(t0)
		m.perf.uiPreps++
	}()
	_, m.nav.histOffset = m.historyWindow()
	if m.mode == modeRollbackConfirm {
		m.setViewContent(m.rollbackFileLines())
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
	case r.cmd.running:
		body = strings.Join(r.cmd.live, "\n")
	case r.cmd.ec != nil:
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
	if r.cmd.running {
		m.output.GotoBottom()
	}
	if cursor >= 0 {
		m.scrollToLine(cursor)
	}
}

// welcomePane hands the boot pane the facts it reports, so nothing in
// ui/welcome reaches into Model.
func (m Model) welcomePane() []string {
	snap := m.sess.UsageSnapshot()
	return welcome.Lines(welcome.Facts{
		Proposer: m.info.Proposer, Judge: m.info.Judge, RunMode: m.info.RunMode,
		Image: m.info.Image, Mount: m.info.Mount,
		Runtime: m.info.Runtime, Network: m.info.Network,
		Goals: snap.Goals, Commands: snap.Commands, MachineTime: snap.MachineTime(),
	}, paneInner(m.layout.outputColW), m.output.Height(), m.welcomeFrame)
}

// rollbackFileLines is every path a revert would touch, in full. The
// viewport scrolls it, so a goal that rewrote a whole tree is still
// reviewable rather than cut off at an arbitrary count.
func (m Model) rollbackFileLines() string {
	width := paneInner(m.layout.outputColW)
	var b strings.Builder
	for i, f := range m.rollback.files {
		if i > 0 {
			b.WriteString("\n")
		}
		mark, style := "restore", styleDiffAdd
		if f.Removed {
			mark, style = "delete ", styleDiffDel
		}
		note := ""
		if f.Unseen {
			note = styleDanger.Render("  ⚠ not detent's")
		}
		b.WriteString(" " + style.Render(mark) + " " +
			styleGoal.Render(layout.Truncate(f.Path, width-24)) + note)
	}
	return b.String()
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
