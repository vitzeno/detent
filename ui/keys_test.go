package ui

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// Tests for keys.go: key routing, focus, and pane navigation.

// TestUI_EnterAcceptsConfirmAndSaveConfirm locks enter as an alternate
// accept alongside y/Y, matching the "[y/enter]" footer text.
func TestUI_EnterAcceptsConfirmAndSaveConfirm(t *testing.T) {
	t.Run("confirm", func(t *testing.T) {
		m := testUIModel()
		m.blocks = []*goalBlock{{goal: "g", res: &GoalResult{Goal: "g"}}}
		m.cur = m.blocks[0]
		m.mode = modeConfirm
		m.confirm.pending.Command = "ls"

		nm, cmd := m.confirmKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = nm.(Model)
		require.Equal(t, modeInput, m.mode, "enter must approve, same as y")
		require.Len(t, m.cur.steps, 1)
		require.NotNil(t, cmd)
	})

	t.Run("save confirm", func(t *testing.T) {
		row, _ := rowWithEditor(t, "old\n")
		row.editor.SetValue("new\n")
		m := testUIModel()
		m.save.row = row
		m.mode = modeSaveConfirm

		nm, cmd := m.saveConfirmKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = nm.(Model)
		require.Equal(t, modeInput, m.mode, "enter must confirm the save, same as y")
		require.NotNil(t, cmd)
	})
}

// TestUI_TypingReachesInput locks space/q/v/j/k landing in the input
// field instead of getting swallowed by navigation.
func TestUI_TypingReachesInput(t *testing.T) {
	m := New(context.Background(), newFakeDriver(), SessionInfo{Proposer: "test-model"})
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()

	for _, k := range []string{"w", "h", "a", "t", "space", "q", "v", "j", "k"} {
		nm, _ := m.Update(typeKey(k))
		m = nm.(Model)
	}
	require.Equal(t, "what qvjk", m.prompt.Value())
	require.Equal(t, modeInput, m.mode, "typing must not change mode or quit")
}

// TestUI_ArrowsDoNotScrollHistoryWhileInputFocused: up/down must stay
// the input's own while focused, not move the history cursor.
func TestUI_ArrowsDoNotScrollHistoryWhileInputFocused(t *testing.T) {
	m := New(context.Background(), newFakeDriver(), SessionInfo{Proposer: "test-model"})
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{{command: "a"}, {command: "b"}}}}
	m.nav.cursor = 1
	m.nav.follow = false
	require.True(t, m.prompt.Focused())

	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = nm.(Model)
	require.Equal(t, 1, m.nav.cursor, "history cursor must not move while typing")

	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = nm.(Model)
	require.Equal(t, 1, m.nav.cursor, "history cursor must not move while typing")
}

// TestUI_JKNoLongerNavigate: j/k do nothing special anywhere; only
// up/down navigate.
func TestUI_JKNoLongerNavigate(t *testing.T) {
	m := New(context.Background(), newFakeDriver(), SessionInfo{Proposer: "test-model"})
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{{command: "a"}, {command: "b"}}}}
	m.prompt.Blur()
	m.nav.focus = focusHistory
	m.nav.cursor = 0
	m.nav.follow = false

	nm, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = nm.(Model)
	require.Equal(t, 0, m.nav.cursor, "j must not move the history cursor")

	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = nm.(Model)
	require.Equal(t, 1, m.nav.cursor, "down still does")
}

// TestUI_NavKeysWorkWhenInputBlurred: arrows move the cursor and space
// toggles while a command runs (input blurred).
func TestUI_NavKeysWorkWhenInputBlurred(t *testing.T) {
	m := New(context.Background(), newFakeDriver(), SessionInfo{Proposer: "test-model"})
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{
		{command: "a"},
		{command: "b"},
	}}}
	m.prompt.Blur()
	m.nav.cursor = 0
	m.nav.follow = false

	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = nm.(Model)
	require.Equal(t, 1, m.nav.cursor)

	nm, _ = m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	m = nm.(Model)
	require.True(t, m.blocks[0].steps[1].cmd.expanded)
}

func TestUI_TabCyclesThreePanes(t *testing.T) {
	m := New(context.Background(), newFakeDriver(), SessionInfo{Proposer: "test-model"})
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	require.Equal(t, focusInput, m.nav.focus)

	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = nm.(Model)
	require.Equal(t, focusHistory, m.nav.focus)
	require.False(t, m.prompt.Focused())

	// "q" is just a letter now — quitting is ctrl+c or /quit, so a
	// stray keystroke outside the input can't end the session.
	nm, cmd := m.Update(typeKey("q"))
	m = nm.(Model)
	require.Nil(t, cmd, "q must not quit")

	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = nm.(Model)
	require.Equal(t, focusOutput, m.nav.focus)

	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = nm.(Model)
	require.Equal(t, focusInput, m.nav.focus)
	require.True(t, m.prompt.Focused())
}

func TestUI_SlashEntryWhileBusy(t *testing.T) {
	m := busyUIModel()

	// Plain text is ignored while busy — no goal can start mid-run.
	nm, _ := m.Update(typeKey("x"))
	m = nm.(Model)
	require.Empty(t, m.prompt.Value())
	require.Empty(t, m.blocks)

	// "/" opens slash entry; further keys complete the dropdown.
	nm, _ = m.Update(typeKey("/"))
	m = nm.(Model)
	require.Equal(t, "/", m.prompt.Value())
	require.Len(t, m.prompt.matches, len(slashCommands))

	nm, _ = m.Update(typeKey("a"))
	m = nm.(Model)
	require.Equal(t, "/a", m.prompt.Value())

	// Enter runs the highlighted entry outright — one press, not two.
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	require.Empty(t, m.prompt.Value(), "running a command clears the box")
	require.Empty(t, m.blocks, "/abort opens no block")
	require.Equal(t, "nothing running", m.notice.text, "the fixture has no command in flight")
}

func TestUI_OutputNavMovesTableCursor(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{tableRow()}}}
	m.nav.cursor = 0
	m.nav.focus = focusOutput

	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = nm.(Model)
	require.Equal(t, 1, m.blocks[0].steps[0].cmd.tableCursor)

	// Clamped at the last row.
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = nm.(Model)
	require.Equal(t, 1, m.blocks[0].steps[0].cmd.tableCursor)

	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = nm.(Model)
	require.Equal(t, 0, m.blocks[0].steps[0].cmd.tableCursor)
}

func TestUI_EscFromOutputReturnsToHistory(t *testing.T) {
	m := testUIModel()
	m.nav.focus = focusOutput
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = nm.(Model)
	require.Equal(t, focusHistory, m.nav.focus)
}

// TestUI_TableScrollNeverMovesHistory: scrolling a table must not
// disturb history position.
func TestUI_TableScrollNeverMovesHistory(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{tableBlock()}
	m.nav.cursor = 0
	m.nav.follow = true
	m.nav.focus = focusOutput
	m.refreshViewport()
	for range 10 {
		nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = nm.(Model)
		_ = m.View().Content
		require.Equal(t, 0, m.nav.cursor, "history cursor must not move")
		require.Equal(t, 0, m.nav.histOffset, "history window must not scroll")
	}
	require.Greater(t, m.blocks[0].steps[0].cmd.tableCursor, 0, "table cursor must move")
}

// Quitting is deliberate: ctrl+c or /quit. "q" used to end the session
// from any pane that wasn't the input, so a stray keystroke while
// reading output threw the session away.
func TestUI_QDoesNotQuit(t *testing.T) {
	for _, focus := range []focusPane{focusHistory, focusOutput} {
		m := testUIModel()
		m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{{command: "ls"}}}}
		m.nav.focus = focus
		m.prompt.Blur()
		m.sizeViewport()

		_, cmd := m.Update(typeKey("q"))
		assert.Nil(t, cmd, "q must not quit from focus %v", focus)
		assert.NotContains(t, m.statusHint(), "[q] quit", "and the hint must not offer it")
	}

	// The ways out still work.
	_, cmd := testUIModel().Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	assert.NotNil(t, cmd, "ctrl+c still quits")
	_, cmd = testUIModel().runSlash("/quit")
	assert.NotNil(t, cmd, "/quit still quits")
}

// slashKey produces a command — tea.Quit for /quit, a goal's batch for
// anything that starts one. Both callers used to discard it, so a
// command picked from the open dropdown did nothing at all.
func TestUI_DropdownEnterCarriesTheCommandBack(t *testing.T) {
	for _, tc := range []struct{ name, typed string }{
		{"idle", "q"},
		{"while a goal runs", "q"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testUIModel()
			if tc.name != "idle" {
				m.prompt.SetValue("some goal")
				nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				m = nm.(Model)
				require.Equal(t, ownerBusy, m.owner())
			}
			m = typeRune(typeRune(m, '/'), rune(tc.typed[0]))
			require.True(t, m.prompt.Open(), "the dropdown must be showing /quit")

			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			assert.NotNil(t, cmd, "enter on the highlighted entry must run it")
		})
	}
}

// esc is intercepted before owner() dispatches, so slashKey's own esc
// case was unreachable and the dropdown stayed open. Escape backs out
// of the innermost thing first: dropdown, then a running goal.
func TestUI_EscapeClosesTheDropdownFirst(t *testing.T) {
	m := typeRune(testUIModel(), '/')
	require.True(t, m.prompt.Open())

	aborted := false
	m.abort = func() { aborted = true }

	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = nm.(Model)
	assert.False(t, m.prompt.Open(), "esc closes the dropdown")
	assert.Equal(t, "/", m.prompt.Value(), "and keeps what was typed")
	assert.False(t, aborted, "the innermost thing goes first, not the running goal")

	// A second esc reaches past it to the goal.
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.True(t, aborted)
	assert.Nil(t, nm.(Model).abort)
}
