package ui

import (
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/ui/editor"
	"github.com/vitzeno/detent/ui/tree"
)

// acceptSlash completes the highlighted entry into the input bar.
func (m Model) acceptSlash() (tea.Model, tea.Cmd) {
	if !m.prompt.Accept() {
		return m, nil
	}
	return m, nil
}

// runSlash handles input-bar commands: quit/abort, plus tool
// invocations (/tree, /usage, /help) — see openTool.
func (m Model) runSlash(cmd string) (tea.Model, tea.Cmd) {
	switch strings.ToLower(strings.Fields(cmd)[0]) {
	case "/q", "/quit":
		return m, tea.Quit
	case "/abort":
		if m.abort != nil {
			m.abort()
			m.abort = nil
			m.notice = "abort sent"
		} else {
			m.notice = "nothing running"
		}
		return m, nil
	case "/tree":
		return m.openTreeTool()
	case "/usage":
		return m.openTool("usage", &stepRow{command: "/usage", toolKind: "usage", tool: toolState{usageExpand: -1}})
	case "/rollback":
		return m.runRollback(cmd)
	case "/help":
		return m.openTool("help", &stepRow{command: "/help", toolKind: "help"})
	default:
		m.notice = "unknown command " + cmd + " (try /help)"
		return m, nil
	}
}

// openTreeTool walks cwd (read-only, never confirmed) and opens it as a
// tool block.
func (m Model) openTreeTool() (tea.Model, tea.Cmd) {
	cwd, err := os.Getwd()
	if err != nil {
		m.notice = "tree: " + err.Error()
		return m, nil
	}
	root, truncated, err := tree.Build(cwd)
	if err != nil {
		m.notice = "tree: " + err.Error()
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
