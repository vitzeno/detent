package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for prompt.go: the growing, wrapping input box and the slash
// dropdown above it.

func TestInputRows_WrapsAndCaps(t *testing.T) {
	assert.Equal(t, 1, inputRows("", 20), "an empty box is still one row")
	assert.Equal(t, 1, inputRows("short", 20))
	assert.Equal(t, 2, inputRows(strings.Repeat("a", 20), 20), "a full row pushes the cursor to the next")
	assert.Equal(t, 2, inputRows(strings.Repeat("a", 25), 20))
	assert.Equal(t, 3, inputRows("one\ntwo\nthree", 20), "hard newlines count too")
	assert.Equal(t, maxInputRows, inputRows(strings.Repeat("x\n", 50), 20), "capped, then it scrolls")
}

func TestInput_GrowsWithContentAndGivesBackSpace(t *testing.T) {
	m := testUIModel()
	require.Equal(t, 1, m.prompt.input.Height(), "starts as a single row")
	tall := m.nav.histHeight

	m.prompt.SetValue(strings.Repeat("wordy goal text ", 60))
	m.sizeViewport()
	grown := m.prompt.input.Height()
	assert.Greater(t, grown, 1, "the box grows with what's typed")
	assert.LessOrEqual(t, grown, maxInputRows)
	assert.Less(t, m.nav.histHeight, tall, "the panes above give up the rows it takes")

	m.prompt.SetValue("")
	m.sizeViewport()
	assert.Equal(t, 1, m.prompt.input.Height(), "and shrink back once it's cleared")
	assert.Equal(t, tall, m.nav.histHeight)
}

// The pane mark sits in its own gutter beside every row. Prefixing it
// onto the block instead would make row one wider than the rest and
// overflow the island, so equal width is what pins the fix.
func TestInput_EveryRowClearsTheMarkGutter(t *testing.T) {
	m := testUIModel()
	m.prompt.SetValue(strings.Repeat("wrapping goal text ", 20))
	m.sizeViewport()

	rows := strings.Split(m.inputBar(), "\n")
	require.Greater(t, len(rows), 1, "the goal must wrap onto more than one row")
	first := lipgloss.Width(stripANSI(rows[0]))
	for i, r := range rows[1:] {
		assert.Equal(t, first, lipgloss.Width(stripANSI(r)), "row %d must match row 0's width", i+1)
	}
	assert.LessOrEqual(t, first, m.layout.width-inputFrameW, "and the block must fit inside the island")
}

func TestInput_EnterSubmitsAndAltEnterInsertsANewline(t *testing.T) {
	m := testUIModel()
	m.prompt.SetValue("first")

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = nm.(Model)
	require.Equal(t, "firstx", m.prompt.Value(), "plain runes type into the box")

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m = nm.(Model)
	assert.Contains(t, m.prompt.Value(), "\n", "alt+enter breaks the line instead of submitting")
	assert.Empty(t, m.blocks, "and starts no goal")

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	assert.Empty(t, m.prompt.Value(), "plain enter submits and clears")
	assert.Len(t, m.blocks, 1)
}

func typeRune(m Model, r rune) Model {
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return nm.(Model)
}

func TestSlashDropdown_OpensFiltersAccepts(t *testing.T) {
	m := testUIModel()
	m = typeRune(m, '/')
	require.Len(t, m.prompt.matches, 6, "bare / lists every command")

	m = typeRune(m, 'q')
	require.Len(t, m.prompt.matches, 1)
	require.Equal(t, "/quit", m.prompt.matches[0].Name)

	// Tab completes into the input bar without running anything.
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = nm.(Model)
	require.Equal(t, "/quit ", m.prompt.Value())
	require.Empty(t, m.prompt.matches, "dropdown closes after accept")
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
	require.Len(t, m.prompt.matches, 6)

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model)
	require.Equal(t, 1, m.prompt.cursor)

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	m = nm.(Model)
	require.Equal(t, 0, m.prompt.cursor)

	// Down on the completed entry accepts it instead of submitting.
	m = typeRune(testUIModel(), '/')
	m = typeRune(m, 'a')
	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.Equal(t, "/abort ", m.prompt.Value())
	require.Empty(t, m.blocks, "partial match must complete, not run")

	// Esc closes the dropdown and keeps the text.
	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	require.Empty(t, m.prompt.matches)
	require.Equal(t, "/abort ", m.prompt.Value())
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
