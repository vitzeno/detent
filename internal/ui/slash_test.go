package ui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func typeRune(m Model, r rune) Model {
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return nm.(Model)
}

func testUIModel() Model {
	m := New(context.Background(), testSession(), "test-model", "")
	m.width, m.height = 120, 40
	m.sizeViewport()
	return m
}

func TestSlashDropdown_OpensFiltersAccepts(t *testing.T) {
	m := testUIModel()
	m = typeRune(m, '/')
	require.Len(t, m.slash, 5, "bare / lists every command")

	m = typeRune(m, 'q')
	require.Len(t, m.slash, 1)
	require.Equal(t, "/quit", m.slash[0].Name)

	// Tab completes into the input bar without running anything.
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = nm.(Model)
	require.Equal(t, "/quit ", m.input.Value())
	require.Empty(t, m.slash, "dropdown closes after accept")
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
	require.Len(t, m.slash, 5)

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model)
	require.Equal(t, 1, m.slashCursor)

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	m = nm.(Model)
	require.Equal(t, 0, m.slashCursor)

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
	require.Empty(t, m.slash)
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
