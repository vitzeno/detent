package ui

import (
	"context"
	"testing"

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

	// In history focus, "q" quits instead of typing.
	nm, cmd := m.Update(typeKey("q"))
	m = nm.(Model)
	require.NotNil(t, cmd, "q in history focus must quit")

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
	require.Len(t, m.prompt.matches, 6)

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
