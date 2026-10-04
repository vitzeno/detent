package ui

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vitzeno/detent/logging"
	"github.com/vitzeno/detent/ui/welcome"
	"github.com/vitzeno/detent/version"
	"github.com/vitzeno/detent/viewspec"
)

// The output pane: what the focused row renders into it, and the
// viewport refresh that keeps it in step.

// scrollMargin is how many lines of context the selection keeps. Two,
// so reaching a table's first row brings its header back into view.
const scrollMargin = 2

// detailLines renders the focused row: a pending question first,
// else the scrolling viewport.
func (m Model) detailLines() []string {
	// The undo list can run to hundreds of paths, so it gets the pane
	// and the box below stays small and asks.
	if m.mode == modeUndo || m.mode == modeForget {
		return strings.Split(m.output.View(), "\n")
	}
	// A panel is a page about the session, drawn through the viewport
	// so it scrolls however long it runs.
	if m.panel.open != panelNone {
		return strings.Split(m.output.View(), "\n")
	}
	if m.showWelcome() {
		return m.welcomeLines()
	}
	return strings.Split(m.output.View(), "\n")
}

// showWelcome reports whether the output pane has nothing of its own
// to show yet, which is the boot state: no row has ever been focused.
func (m Model) showWelcome() bool {
	return m.focused() == nil
}

// welcomeLines hands the boot pane the facts it reports, so nothing in
// ui/welcome reaches into Model.
func (m Model) welcomeLines() []string {
	return welcome.Lines(welcome.Facts{
		Version: version.String(),
		Model:   m.run.Model, Judge: m.run.Judge, RunMode: m.runMode(),
		OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), WorkDir: m.workDir,
		Image: m.info.Image, Mount: m.info.Mount,
		Runtime: m.info.Runtime, Network: m.info.Network,
		Turns: len(m.blocks), ToolCalls: m.calls, Sessions: len(m.sessions),
		Session: m.run.Session.String(), Resumed: m.run.Resumed, Recorded: m.run.Recorded,
	}, paneInner(m.layout.outputColW), m.output.Height(), m.welcomeFrame)
}

// refreshViewport redraws the output pane when its detailKey changed.
func (m *Model) refreshViewport() {
	window, offset := m.historyWindow()
	m.nav.histWindow = window
	// -1 means following, which never counted a total to offset into.
	if offset >= 0 {
		m.nav.histOffset = offset
	}
	// Scrolling moves the viewport, not the content, so it must not redraw.
	key := m.detailKey()
	if key == m.detail {
		return
	}
	m.detail = key
	if m.panel.open != panelNone {
		m.setViewContent(strings.Join(m.panelLines(), "\n"))
		return
	}
	if m.mode == modeForget {
		m.setViewContent(strings.Join(m.forgetLines(), "\n"))
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
	if r.signin != nil {
		m.setViewContent(strings.Join(signInPageLines(r.signin, paneInner(m.layout.outputColW)), "\n"))
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

// setViewContent skips identical content: SetContent resets scroll
// position, which must not happen just from resizing for the dropdown.
func (m *Model) setViewContent(s string) {
	s = wrapWide(s, m.output.Width())
	if s == m.viewContent {
		return
	}
	m.viewContent = s
	m.output.SetContent(s)
}

// wrapWide wraps any line wider than the pane, so a page or live output reads
// whole rather than running off the edge. A view's lines already fit.
func wrapWide(s string, width int) string {
	if width <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, painter{}.Wrap(l, width)...)
	}
	return strings.Join(out, "\n")
}

// viewBody draws the row's view whole. Height lets a plot grow into the
// pane but clips nothing, so a view scrolls like any other output.
func (m Model) viewBody(r *historyRow) (viewspec.Render, bool) {
	return m.drawRow(r, paneInner(m.layout.outputColW), m.output.Height(), m.nav.focus == focusOutput)
}

// drawRow draws a row's view at any size, for the output pane and the
// finder's preview alike, so a row looks the same wherever it is shown.
func (m Model) drawRow(r *historyRow, width, height int, focused bool) (viewspec.Render, bool) {
	b, ok := boundView(r)
	if !ok {
		return viewspec.Render{}, false
	}
	out, err := drawSafely(b, viewspec.Frame{
		Width:   width,
		Height:  height,
		Focused: focused,
		Cursor:  r.tableCursor,
		Paint:   painter{},
	})
	if err != nil {
		// The output is still worth reading when the view of it is not.
		logging.For(logging.UI).Warn("a view could not draw this output",
			logging.KeyEvent, logging.ViewInvalid, logging.KeyReason, err.Error())
		return viewspec.Render{Lines: strings.Split(r.text(), "\n"), CursorLine: -1}, true
	}
	return out, true
}

// drawSafely turns a widget's panic into an error, so one bad view
// cannot take the whole TUI down with it.
func drawSafely(b *viewspec.Bound, f viewspec.Frame) (out viewspec.Render, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("the view panicked: %v", p)
		}
	}()
	return b.Draw(f)
}

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

// tildePath trades a home prefix for ~, so a deep working directory
// still fits the pane. Whole path elements only.
func tildePath(dir, home string) string {
	if home == "" {
		return dir
	}
	if dir == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(dir, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return dir
}
