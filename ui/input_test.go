package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for input.go: the growing, wrapping input box.

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
	require.Equal(t, 1, m.input.Height(), "starts as a single row")
	tall := m.nav.histHeight

	m.input.SetValue(strings.Repeat("wordy goal text ", 60))
	m.sizeViewport()
	grown := m.input.Height()
	assert.Greater(t, grown, 1, "the box grows with what's typed")
	assert.LessOrEqual(t, grown, maxInputRows)
	assert.Less(t, m.nav.histHeight, tall, "the panes above give up the rows it takes")

	m.input.SetValue("")
	m.sizeViewport()
	assert.Equal(t, 1, m.input.Height(), "and shrink back once it's cleared")
	assert.Equal(t, tall, m.nav.histHeight)
}

// The pane mark sits in its own gutter beside every row. Prefixing it
// onto the block instead would make row one wider than the rest and
// overflow the island, so equal width is what pins the fix.
func TestInput_EveryRowClearsTheMarkGutter(t *testing.T) {
	m := testUIModel()
	m.input.SetValue(strings.Repeat("wrapping goal text ", 20))
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
	m.input.SetValue("first")

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = nm.(Model)
	require.Equal(t, "firstx", m.input.Value(), "plain runes type into the box")

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m = nm.(Model)
	assert.Contains(t, m.input.Value(), "\n", "alt+enter breaks the line instead of submitting")
	assert.Empty(t, m.blocks, "and starts no goal")

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	assert.Empty(t, m.input.Value(), "plain enter submits and clears")
	assert.Len(t, m.blocks, 1)
}
