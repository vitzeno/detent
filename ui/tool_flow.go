package ui

import (
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/ui/editor"
	"github.com/vitzeno/detent/ui/tree"
)

// runSlash dispatches through slash.go's registry. A handler that sets
// no notice has succeeded and gets a green flash here, so none of them
// has to remember one.
func (m Model) runSlash(input string) (tea.Model, tea.Cmd) {
	c, ok := lookupSlash(input)
	if !ok {
		m.noteErr("unknown command " + input + " (try /help)")
		return m, nil
	}
	next, cmd := c.run(m, input)
	// A command that opened a confirm hasn't finished, so it hasn't
	// succeeded either — its own outcome comes once it's answered.
	if nm, isModel := next.(Model); isModel && nm.notice.text == "" && nm.mode == modeInput {
		nm.noteOK(c.Name)
		return nm, cmd
	}
	return next, cmd
}

// acceptSlash completes the highlighted entry into the input bar.
func (m Model) acceptSlash() (tea.Model, tea.Cmd) {
	m.prompt.Accept()
	return m, nil
}

// abortRunning cancels the command in flight, if there is one.
func (m Model) abortRunning(string) (tea.Model, tea.Cmd) {
	if m.abort == nil {
		m.noteErr("nothing running")
		return m, nil
	}
	m.abort()
	m.abort = nil
	m.noteOK("abort sent")
	return m, nil
}

// openTreeTool walks cwd (read-only, never confirmed) and opens it as a
// tool block.
func (m Model) openTreeTool() (tea.Model, tea.Cmd) {
	cwd, err := os.Getwd()
	if err != nil {
		m.noteErr("tree: " + err.Error())
		return m, nil
	}
	root, truncated, err := tree.Build(cwd)
	if err != nil {
		m.noteErr("tree: " + err.Error())
		return m, nil
	}
	tm := tree.New(root)
	command := cwd
	if truncated {
		command += " (truncated)"
	}
	return m.openTool("tree", &stepRow{command: command, toolKind: "tree", tool: toolState{tree: &tm}})
}

// openTreeSelection: a directory toggles expand/collapse, a file opens
// as its own new history entry showing its editor.
func (m Model) openTreeSelection(r *stepRow) (tea.Model, tea.Cmd) {
	if r.tool.tree == nil {
		return m, nil
	}
	n := r.tool.tree.Selected()
	if n == nil {
		return m, nil
	}
	if n.Kind == tree.KindDir {
		r.tool.tree.Toggle()
		return m, nil
	}
	content, truncated, maxBytes, err := m.sess.ReadFile(n.Path)
	ed := editor.New(n.Path, content, truncated, maxBytes, err)
	fileRow := &stepRow{command: n.Path, editPath: n.Path, toolKind: "file", editor: &ed}
	return m.openTool("file", fileRow)
}

// openTool appends a tool block and jumps straight to viewing it — an
// explicit human action, unlike trackNewest's background-event default.
func (m Model) openTool(kind string, row *stepRow) (tea.Model, tea.Cmd) {
	m.blocks = append(m.blocks, &goalBlock{tool: kind, ended: true, steps: []*stepRow{row}})
	m.nav.cursor = len(m.rows()) - 1
	m.nav.focus = focusOutput
	return m, nil
}

// startOver empties the screen and the session behind it. The welcome
// pane needs nothing of its own: it shows whenever no row is focused,
// so dropping the blocks brings it back — along with its ticker, which
// stopped when the first row appeared.
func (m Model) startOver(string) (tea.Model, tea.Cmd) {
	if m.waiting {
		m.noteErr("/new: busy, try again once the current step finishes")
		return m, nil
	}
	m.sess.Reset()
	m.blocks = nil
	m.cur = nil
	m.totalCmds = 0
	m.panel = panelState{}
	m.counts = counters{}
	m.nav = navState{follow: true, histHeight: m.nav.histHeight}
	m.mode = modeInput
	m.prompt.Clear()
	m.prompt.Focus()
	m.noteOK("new session")
	return m, welcomeTick()
}
