package ui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/agent"
)

// Tests for keys.go: key routing, focus, and pane navigation.

// TestUI_EnterAcceptsConfirmAndSaveConfirm locks enter as an alternate
// accept alongside y/Y on both dialog modes, matching what their own
// footer text now advertises ("[y/enter]").
func TestUI_EnterAcceptsConfirmAndSaveConfirm(t *testing.T) {
	t.Run("confirm", func(t *testing.T) {
		m := testUIModel()
		m.blocks = []*goalBlock{{goal: "g", res: &agent.GoalResult{Goal: "g"}}}
		m.cur = m.blocks[0]
		m.mode = modeConfirm
		m.confirm.pending.Command = "ls"

		nm, cmd := m.confirmKey(tea.KeyMsg{Type: tea.KeyEnter})
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

		nm, cmd := m.saveConfirmKey(tea.KeyMsg{Type: tea.KeyEnter})
		m = nm.(Model)
		require.Equal(t, modeInput, m.mode, "enter must confirm the save, same as y")
		require.NotNil(t, cmd)
	})
}

// TestUI_TypingReachesInput is the regression test for space (and q/v/j/k)
// being swallowed by navigation: with a focused input, every text key
// must land in the goal field.
func TestUI_TypingReachesInput(t *testing.T) {
	m := New(context.Background(), testSession(), "test-model", "")
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()

	for _, k := range []string{"w", "h", "a", "t", "space", "q", "v", "j", "k"} {
		nm, _ := m.handleKey(typeKey(k))
		m = nm.(Model)
	}
	require.Equal(t, "what qvjk", m.input.Value())
	require.Equal(t, modeInput, m.mode, "typing must not change mode or quit")
}

// TestUI_ArrowsDoNotScrollHistoryWhileInputFocused is the reported bug,
// locked: with the input focused, up/down must be the input's own
// (bubbles/textinput no-ops them absent suggestions) rather than
// silently moving the history cursor out from under whatever's typed.
func TestUI_ArrowsDoNotScrollHistoryWhileInputFocused(t *testing.T) {
	m := New(context.Background(), testSession(), "test-model", "")
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{{command: "a"}, {command: "b"}}}}
	m.nav.cursor = 1
	m.nav.follow = false
	require.True(t, m.input.Focused())

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	m = nm.(Model)
	require.Equal(t, 1, m.nav.cursor, "history cursor must not move while typing")

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model)
	require.Equal(t, 1, m.nav.cursor, "history cursor must not move while typing")
}

// TestUI_JKNoLongerNavigate is the counterpart to the arrow-keys-only
// preference: j/k must do nothing special anywhere navigation used to
// accept them — only up/down do.
func TestUI_JKNoLongerNavigate(t *testing.T) {
	m := New(context.Background(), testSession(), "test-model", "")
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{{command: "a"}, {command: "b"}}}}
	m.input.Blur()
	m.nav.focus = focusHistory
	m.nav.cursor = 0
	m.nav.follow = false

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = nm.(Model)
	require.Equal(t, 0, m.nav.cursor, "j must not move the history cursor")

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model)
	require.Equal(t, 1, m.nav.cursor, "down still does")
}

// TestUI_NavKeysWorkWhenInputBlurred ensures navigation still works while
// a command runs (input blurred): arrows move the cursor, space toggles.
func TestUI_NavKeysWorkWhenInputBlurred(t *testing.T) {
	m := New(context.Background(), testSession(), "test-model", "")
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{
		{command: "a"},
		{command: "b"},
	}}}
	m.input.Blur()
	m.nav.cursor = 0
	m.nav.follow = false

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model)
	require.Equal(t, 1, m.nav.cursor)

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	m = nm.(Model)
	require.True(t, m.blocks[0].steps[1].cmd.expanded)
}

func TestUI_TabCyclesThreePanes(t *testing.T) {
	m := New(context.Background(), testSession(), "test-model", "")
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	require.Equal(t, focusInput, m.nav.focus)

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = nm.(Model)
	require.Equal(t, focusHistory, m.nav.focus)
	require.False(t, m.input.Focused())

	// In history focus, "q" quits instead of typing.
	nm, cmd := m.handleKey(typeKey("q"))
	m = nm.(Model)
	require.NotNil(t, cmd, "q in history focus must quit")

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = nm.(Model)
	require.Equal(t, focusOutput, m.nav.focus)

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = nm.(Model)
	require.Equal(t, focusInput, m.nav.focus)
	require.True(t, m.input.Focused())
}

func TestUI_SlashEntryWhileBusy(t *testing.T) {
	m := busyUIModel()

	// Plain text is ignored while busy — no goal can start mid-run.
	nm, _ := m.handleKey(typeKey("x"))
	m = nm.(Model)
	require.Empty(t, m.input.Value())
	require.Empty(t, m.blocks)

	// "/" opens slash entry; further keys complete the dropdown.
	nm, _ = m.handleKey(typeKey("/"))
	m = nm.(Model)
	require.Equal(t, "/", m.input.Value())
	require.Len(t, m.slash.matches, 5)

	nm, _ = m.handleKey(typeKey("a"))
	m = nm.(Model)
	require.Equal(t, "/a", m.input.Value())

	// Partial + enter completes instead of submitting.
	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.Equal(t, "/abort ", m.input.Value())
	require.Empty(t, m.blocks)
}

func TestUI_OutputNavMovesTableCursor(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{tableRow()}}}
	m.nav.cursor = 0
	m.nav.focus = focusOutput

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model)
	require.Equal(t, 1, m.blocks[0].steps[0].cmd.tableCursor)

	// Clamped at the last row.
	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model)
	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model)
	require.Equal(t, 1, m.blocks[0].steps[0].cmd.tableCursor)

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	m = nm.(Model)
	require.Equal(t, 0, m.blocks[0].steps[0].cmd.tableCursor)
}

func TestUI_EscFromOutputReturnsToHistory(t *testing.T) {
	m := testUIModel()
	m.nav.focus = focusOutput
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	require.Equal(t, focusHistory, m.nav.focus)
}

// TestUI_TableScrollNeverMovesHistory is the reported bug, locked:
// scrolling inside an output table must not disturb history position.
func TestUI_TableScrollNeverMovesHistory(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{tableBlock()}
	m.nav.cursor = 0
	m.nav.follow = true
	m.nav.focus = focusOutput
	m.refreshViewport()
	for range 10 {
		nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
		m = nm.(Model)
		_ = m.View()
		require.Equal(t, 0, m.nav.cursor, "history cursor must not move")
		require.Equal(t, 0, m.nav.histOffset, "history window must not scroll")
	}
	require.Greater(t, m.blocks[0].steps[0].cmd.tableCursor, 0, "table cursor must move")
}
