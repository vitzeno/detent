package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func typeRune(m Model, r rune) Model {
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return nm.(Model)
}

func TestSlashDropdown_OpensFiltersAccepts(t *testing.T) {
	m := testUIModel()
	m = typeRune(m, '/')
	require.Len(t, m.slash.matches, 6, "bare / lists every command")

	m = typeRune(m, 'q')
	require.Len(t, m.slash.matches, 1)
	require.Equal(t, "/quit", m.slash.matches[0].Name)

	// Tab completes into the input bar without running anything.
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = nm.(Model)
	require.Equal(t, "/quit ", m.input.Value())
	require.Empty(t, m.slash.matches, "dropdown closes after accept")
	require.Empty(t, m.blocks)

	// Enter on the exact command runs it.
	nm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.NotNil(t, cmd, "/quit must quit")
	require.Empty(t, m.blocks)
}

func TestSlashDropdown_NavigateAndEsc(t *testing.T) {
	m := testUIModel()
	m = typeRune(m, '/')
	require.Len(t, m.slash.matches, 6)

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model)
	require.Equal(t, 1, m.slash.cursor)

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	m = nm.(Model)
	require.Equal(t, 0, m.slash.cursor)

	// Down on the completed entry accepts it instead of submitting.
	m = typeRune(testUIModel(), '/')
	m = typeRune(m, 'a')
	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.Equal(t, "/abort ", m.input.Value())
	require.Empty(t, m.blocks, "partial match must complete, not run")

	// Esc closes the dropdown and keeps the text.
	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	require.Empty(t, m.slash.matches)
	require.Equal(t, "/abort ", m.input.Value())
}

func TestSlashDropdown_RendersAboveInput(t *testing.T) {
	m := testUIModel()
	m = typeRune(m, '/')
	v := m.View()
	assert.Contains(t, v, "/quit")
	assert.Contains(t, v, "/abort")
	assert.Contains(t, v, "/help")
}

// TestSlashDropdown_RowsAlign guards the whole rendered view, not just
// slash.View: the dropdown is multi-line, so anything prefixed onto it
// (the pane mark, once) indents only its first row.
func TestSlashDropdown_RowsAlign(t *testing.T) {
	m := testUIModel()
	m = typeRune(m, '/')

	var cols []int
	for _, line := range strings.Split(m.View(), "\n") {
		for _, name := range []string{"/quit", "/abort", "/tree"} {
			if c := displayCol(stripANSI(line), name); c >= 0 {
				cols = append(cols, c)
			}
		}
	}
	require.Len(t, cols, 3, "every dropdown row must render")
	assert.Equal(t, cols[0], cols[1], "cursor row must start in the same column as the rest")
	assert.Equal(t, cols[1], cols[2])
}
