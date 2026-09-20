// Package tree builds and renders a navigable, read-only file/directory
// tree. It never writes; opening a selected file for editing is the
// caller's job.
package tree

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/vitzeno/detent/internal/ui/theme"
)

var (
	cursorStyle = lipgloss.NewStyle().Foreground(theme.Accent).Bold(true)
	dirStyle    = lipgloss.NewStyle().Foreground(theme.TextPrimary).Bold(true)
	fileStyle   = lipgloss.NewStyle().Foreground(theme.TextMuted)
	glyphStyle  = lipgloss.NewStyle().Foreground(theme.TextFaint)
)

// Kind distinguishes a file node from a directory node.
type Kind int

const (
	KindFile Kind = iota
	KindDir
)

// MaxDepth and MaxEntries bound one Build call so a huge tree
// (node_modules, a build dir) degrades instead of hanging the UI.
const (
	MaxDepth   = 6
	MaxEntries = 500
)

// Node is one file or directory in the tree.
type Node struct {
	Name     string
	Path     string
	Kind     Kind
	Children []*Node
	Expanded bool
}

// Build walks root into a Node tree, skipping dotfiles/dotdirs, capped
// at MaxDepth/MaxEntries. The returned bool means the walk hit a bound
// and stopped early, not that it failed.
func Build(root string) (*Node, bool, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, false, fmt.Errorf("tree: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, false, fmt.Errorf("tree: %w", err)
	}
	if !info.IsDir() {
		return nil, false, fmt.Errorf("tree: %s is not a directory", abs)
	}
	root0 := &Node{Name: filepath.Base(abs), Path: abs, Kind: KindDir, Expanded: true}
	n := 1
	truncated := walk(root0, 1, &n)
	return root0, truncated, nil
}

func walk(n *Node, depth int, budget *int) bool {
	if depth > MaxDepth {
		return true
	}
	entries, err := os.ReadDir(n.Path)
	if err != nil {
		return false // unreadable dir: show it with no children, not an error
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir() // directories first
		}
		return entries[i].Name() < entries[j].Name()
	})

	truncated := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if *budget >= MaxEntries {
			return true
		}
		*budget++
		child := &Node{Name: e.Name(), Path: filepath.Join(n.Path, e.Name())}
		if e.IsDir() {
			child.Kind = KindDir
			if walk(child, depth+1, budget) {
				truncated = true
			}
		} else {
			child.Kind = KindFile
		}
		n.Children = append(n.Children, child)
	}
	return truncated
}

// Model is the interactive, navigable view over a Node tree.
type Model struct {
	root   *Node
	rows   []*Node
	depths []int
	cursor int
}

// New builds a Model over an already-built tree (see Build).
func New(root *Node) Model {
	m := Model{root: root}
	m.refresh()
	return m
}

func (m *Model) refresh() {
	m.rows, m.depths = nil, nil
	var visit func(n *Node, depth int)
	visit = func(n *Node, depth int) {
		m.rows = append(m.rows, n)
		m.depths = append(m.depths, depth)
		if n.Kind == KindDir && n.Expanded {
			for _, c := range n.Children {
				visit(c, depth+1)
			}
		}
	}
	visit(m.root, 0)
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
}

// Up/Down move the cursor among currently visible rows.
func (m *Model) Up() {
	if m.cursor > 0 {
		m.cursor--
	}
}

func (m *Model) Down() {
	if m.cursor < len(m.rows)-1 {
		m.cursor++
	}
}

// Toggle expands/collapses the cursor row if it's a directory; does
// nothing for a file (opening one is the caller's job via Selected).
func (m *Model) Toggle() {
	n := m.Selected()
	if n == nil || n.Kind != KindDir {
		return
	}
	n.Expanded = !n.Expanded
	m.refresh()
}

// Selected is the node under the cursor, or nil for an empty tree.
func (m Model) Selected() *Node {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return m.rows[m.cursor]
}

// View renders height rows (cursor kept in view), each truncated to width.
func (m Model) View(width, height int) []string {
	if len(m.rows) == 0 {
		return []string{"(empty)"}
	}
	start := 0
	if m.cursor >= height {
		start = m.cursor - height + 1
	}
	end := min(start+height, len(m.rows))

	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, m.line(i, width))
	}
	return out
}

func (m Model) line(i, width int) string {
	n, depth := m.rows[i], m.depths[i]
	mark := "  "
	if i == m.cursor {
		mark = cursorStyle.Render("▸ ")
	}
	icon := "  "
	name := fileStyle.Render(n.Name)
	if n.Kind == KindDir {
		icon = "▾ "
		if !n.Expanded {
			icon = "▸ "
		}
		name = dirStyle.Render(n.Name)
	}
	s := mark + strings.Repeat("  ", depth) + glyphStyle.Render(icon) + name
	if lipgloss.Width(s) > width {
		s = ansi.Truncate(s, width, "…") // rune-cut would corrupt the styling codes
	}
	return s
}
