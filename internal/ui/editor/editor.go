// Package editor is a self-contained, reusable text-editor component:
// load a file, let the user edit the in-memory buffer, report a dirty
// diff against disk. It never writes anything itself — Save is always
// the caller's own explicit, confirmed action (see internal/fileio
// and the ui package's save-confirm flow) — this package only ever
// reads. Any caller that wants an editable view of a file uses the
// same Model, whether it got there from a command's own proposed file
// write or from browsing a tree.
package editor

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vitzeno/detent/internal/fileio"
	"github.com/vitzeno/detent/internal/ui/theme"
)

// Model is one open file's edit state: the textarea buffer plus what's
// actually confirmed on disk (saved), so Dirty/Diff always compare
// against ground truth, not just whatever Read happened to return once.
type Model struct {
	Path      string
	area      textarea.Model
	saved     string // last content confirmed written to disk
	truncated bool   // Read reported the file was capped at fileio.MaxBytes
	loadErr   error

	// Cache so SetWidth/SetHeight (which can disturb the textarea's own
	// scroll state) are only called when the size actually changed.
	lastW, lastH int
}

// New loads path and wraps it in a ready-to-render textarea. A load
// error is kept on the Model, not returned — the caller still gets a
// usable component that reports the error in place of content, the
// same way a failed command still gets a row rather than vanishing.
func New(path string) Model {
	content, truncated, err := fileio.Read(path)
	ta := textarea.New()
	ta.SetValue(content)
	ta.ShowLineNumbers = true
	ta.Prompt = ""
	ta.Placeholder = ""
	applyTheme(&ta)
	return Model{Path: path, area: ta, saved: content, truncated: truncated, loadErr: err}
}

// applyTheme brings the textarea in line with the rest of the app's
// palette — bubbles' own defaults have no idea this app has a theme at
// all, so left alone the editor is the one component that looks
// visually disconnected from everything around it.
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

// Err is non-nil when the initial Read failed; View reports it as
// content instead of crashing or showing an empty buffer.
func (m Model) Err() error { return m.loadErr }

// Truncated reports whether the file was larger than fileio.MaxBytes
// and got capped.
func (m Model) Truncated() bool { return m.truncated }

// Value is the buffer's current content, edits included.
func (m Model) Value() string { return m.area.Value() }

// SetValue replaces the buffer's content programmatically — for a
// caller (or a test) that wants to set content directly rather than
// simulate every keystroke.
func (m *Model) SetValue(s string) { m.area.SetValue(s) }

// Dirty reports whether the buffer differs from what's confirmed saved.
func (m Model) Dirty() bool {
	return m.loadErr == nil && m.area.Value() != m.saved
}

// Diff renders a unified diff of what a save right now would change,
// or "" when there's nothing to save.
func (m Model) Diff() string {
	return fileio.Diff(m.Path, m.saved, m.area.Value())
}

// MarkSaved updates what "saved" means after the caller's own
// confirmed write succeeds — Dirty and Diff compare against this from
// then on. The caller passes back exactly what it wrote, so this Model
// never has to assume its own Value() is what actually landed on disk.
func (m *Model) MarkSaved(content string) {
	m.saved = content
}

// Resize applies w/h only when they changed, so the textarea's own
// scroll state isn't disturbed on every render.
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

// Focus/Blur mirror textarea's own — Focus is needed for the cursor to
// render and for keys to have visible effect.
func (m *Model) Focus() tea.Cmd { return m.area.Focus() }
func (m *Model) Blur()          { m.area.Blur() }

// Update forwards a key (or any bubbletea msg) to the textarea. The
// caller decides when that's appropriate — this component has no
// opinion on focus/ownership beyond rendering and editing.
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
