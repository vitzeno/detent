package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

// Every modifier+enter breaks the line; only bare enter submits.
// shift+enter is the one users reach for, but it only arrives as its
// own key on a terminal speaking the Kitty protocol, so the fallbacks
// stay bound rather than being replaced.
func TestInput_EnterSubmitsAndModifiedEnterInsertsANewline(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"shift+enter", tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}},
		{"alt+enter", tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}},
		{"ctrl+j", tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testUIModel()
			m.prompt.SetValue("first")

			nm, _ := m.Update(tc.key)
			m = nm.(Model)
			assert.Contains(t, m.prompt.Value(), "\n", "must break the line, not submit")
			assert.Empty(t, m.blocks, "and start no goal")
		})
	}

	m := testUIModel()
	m.prompt.SetValue("first")

	nm, _ := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = nm.(Model)
	require.Equal(t, "firstx", m.prompt.Value(), "plain runes type into the box")

	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	assert.Empty(t, m.prompt.Value(), "plain enter submits and clears")
	assert.Len(t, m.blocks, 1)
}

// The hint must name a key that works on this terminal: without the
// Kitty protocol shift+enter is indistinguishable from enter, so
// advertising it would be a lie.
func TestInput_NewlineHintFollowsWhatTheTerminalSupports(t *testing.T) {
	m := testUIModel()
	assert.Contains(t, m.statusHint(), "[alt+enter] newline", "nothing negotiated yet")

	nm, _ := m.Update(tea.KeyboardEnhancementsMsg{Flags: 1})
	m = nm.(Model)
	assert.Contains(t, m.statusHint(), "[shift+enter] newline")

	nm, _ = m.Update(tea.KeyboardEnhancementsMsg{})
	m = nm.(Model)
	assert.Contains(t, m.statusHint(), "[alt+enter] newline", "a terminal that declined falls back")
}

func typeRune(m Model, r rune) Model {
	nm, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
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
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = nm.(Model)
	require.Equal(t, "/quit ", m.prompt.Value())
	require.Empty(t, m.prompt.matches, "dropdown closes after accept")
	require.Empty(t, m.blocks)

	// Enter on the exact command runs it.
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	require.NotNil(t, cmd, "/quit must quit")
	require.Empty(t, m.blocks)
}

func TestSlashDropdown_NavigateAndEsc(t *testing.T) {
	m := testUIModel()
	m = typeRune(m, '/')
	require.Len(t, m.prompt.matches, 6)

	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = nm.(Model)
	require.Equal(t, 1, m.prompt.cursor)

	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = nm.(Model)
	require.Equal(t, 0, m.prompt.cursor)

	// Enter on a partial match runs the highlighted entry, rather than
	// completing it and making the human press enter a second time.
	m = typeRune(testUIModel(), '/')
	m = typeRune(m, 'a')
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	require.Empty(t, m.prompt.Value(), "running a command clears the box")
	require.Equal(t, "nothing running", m.notice, "/abort ran")

	// Esc closes the dropdown and keeps the text; tab is what completes.
	m = typeRune(testUIModel(), '/')
	m = typeRune(m, 'a')
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = nm.(Model)
	require.Equal(t, "/abort ", m.prompt.Value())
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = nm.(Model)
	require.Empty(t, m.prompt.matches)
	require.Equal(t, "/abort ", m.prompt.Value())
}

func TestSlashDropdown_RendersAboveInput(t *testing.T) {
	m := testUIModel()
	m = typeRune(m, '/')
	v := m.View().Content
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
	for _, line := range strings.Split(m.View().Content, "\n") {
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

func TestSlashRegistry_MatchAndExact(t *testing.T) {
	assert.Len(t, matchSlash("/"), 6)
	require.Len(t, matchSlash("/q"), 1)
	assert.Equal(t, "/quit", matchSlash("/q")[0].Name)
	assert.Empty(t, matchSlash("/x"))
	assert.Empty(t, matchSlash("quit"), "no leading slash matches nothing")
	assert.True(t, exactSlash("/quit"))
	assert.True(t, exactSlash("/rollback 2"), "an argument still names the command")
	assert.False(t, exactSlash("/q"))
}

// Every listed command must dispatch, and every dispatchable command
// must be listed — the two used to live in different packages and had
// already drifted (/q ran but never appeared anywhere).
func TestSlashRegistry_EveryCommandRuns(t *testing.T) {
	for _, c := range slashCommands {
		t.Run(c.Name, func(t *testing.T) {
			require.NotNil(t, c.run, "registered without a handler")
			require.NotEmpty(t, c.Desc, "registered without a description")

			m := testUIModel()
			nm, _ := m.runSlash(c.Name)
			assert.NotContains(t, nm.(Model).notice, "unknown command")
		})
	}
}

// TestSlashDropdown_PadsBeforeStyling guards against padding a name
// after it's been ANSI-styled: fmt's width verbs count escape bytes
// too, so %-10s applied post-Render silently drops the padding.
func TestSlashDropdown_PadsBeforeStyling(t *testing.T) {
	v := slashDropdown(matchSlash("/"), 0)
	require.Contains(t, v, "\x1b[", "lipgloss must style for this test to mean anything")
	plain := stripANSI(v)
	assert.Contains(t, plain, "▸ /quit      quit detent")
	assert.Contains(t, plain, "  /abort     abort the running command")
}
