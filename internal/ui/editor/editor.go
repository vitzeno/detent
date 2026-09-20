// Package editor wraps a file's already-loaded content in an editable
// buffer and diffs it against disk. It never reads or writes a file
// itself — the caller owns the initial read and the confirmed write.
package editor

import (
	"github.com/aymanbagabas/go-udiff"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vitzeno/detent/internal/ui/theme"
)

// Model is one open file's edit state. saved tracks what's actually on
// disk so Dirty/Diff compare against ground truth, not a stale read.
type Model struct {
	Path      string
	area      textarea.Model
	saved     string // last content confirmed written to disk
	truncated bool
	maxBytes  int // cap truncated was measured against
	loadErr   error

	lastW, lastH int // skip SetWidth/SetHeight when unchanged, keeps scroll position
}

// New wraps path's already-loaded content in a ready-to-render textarea.
// loadErr is kept on the Model rather than returned, so the caller
// still gets a usable component that shows the error in place of content.
func New(path, content string, truncated bool, maxBytes int, loadErr error) Model {
	ta := textarea.New()
	ta.SetValue(content)
	ta.ShowLineNumbers = true
	ta.Prompt = ""
	ta.Placeholder = ""
	applyTheme(&ta)
	return Model{Path: path, area: ta, saved: content, truncated: truncated, maxBytes: maxBytes, loadErr: loadErr}
}

// applyTheme brings the textarea in line with the app's palette —
// bubbles' own defaults know nothing about it.
func applyTheme(ta *textarea.Model) {
	lineNumber := lipgloss.NewStyle().Foreground(theme.TextFaint)
	currentLineNumber := lipgloss.NewStyle().Foreground(theme.Accent).Bold(true)
	endOfBuffer := lipgloss.NewStyle().Foreground(theme.TextFaint)

	for _, s := range []*textarea.Style{&ta.FocusedStyle, &ta.BlurredStyle} {
		s.LineNumber = lineNumber
		s.CursorLineNumber = currentLineNumber
		s.EndOfBuffer = endOfBuffer
	}
	ta.Cursor.Style = lipgloss.NewStyle().Foreground(theme.Accent)
}

// Err is non-nil when the initial read failed; View shows it in place
// of content instead of crashing or going blank.
func (m Model) Err() error { return m.loadErr }

func (m Model) Truncated() bool { return m.truncated }
func (m Model) MaxBytes() int   { return m.maxBytes }
func (m Model) Value() string   { return m.area.Value() }

// SetValue sets content directly, for a caller or test that doesn't
// want to simulate every keystroke.
func (m *Model) SetValue(s string) { m.area.SetValue(s) }

// Dirty reports whether the buffer differs from what's confirmed saved.
func (m Model) Dirty() bool {
	return m.loadErr == nil && m.area.Value() != m.saved
}

// Diff renders a unified diff of what a save right now would change,
// or "" when there's nothing to save.
func (m Model) Diff() string {
	if m.saved == m.area.Value() {
		return ""
	}
	return udiff.Unified(m.Path, m.Path, m.saved, m.area.Value())
}

// MarkSaved updates what "saved" means after a confirmed write. The
// caller passes back exactly what it wrote, rather than us assuming
// Value() is what landed on disk.
func (m *Model) MarkSaved(content string) {
	m.saved = content
}

func (m *Model) Resize(w, h int) {
	if m.lastW != w {
		m.area.SetWidth(w)
		m.lastW = w
	}
	if m.lastH != h {
		m.area.SetHeight(h)
		m.lastH = h
	}
}

func (m *Model) Focus() tea.Cmd { return m.area.Focus() }
func (m *Model) Blur()          { m.area.Blur() }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.area, cmd = m.area.Update(msg)
	return m, cmd
}

// View renders the buffer, or the load error in its place.
func (m Model) View() string {
	if m.loadErr != nil {
		return "could not open " + m.Path + ": " + m.loadErr.Error()
	}
	return m.area.View()
}
