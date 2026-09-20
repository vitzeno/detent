package ui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/shell"
)

// Tests for view.go: rendering, sizing, and the detail-zone component
// dispatch. render_view.go's own transforms (errorSeverity, diffClass,
// prettyJSON) have their own render_view_test.go.

// TestUpdateHistoryWindow_PersistsOffsetAcrossRenders locks the fix for
// a bug where the windowing math used to live in a value-receiver
// method reached only from View()'s own call chain — Bubble Tea always
// renders View() on a throwaway copy of Model, so its writes to
// histOffset/cursorLine never reached the real Model, leaving the
// window stuck at offset zero and forcing the cursor to the window's
// last line on every render instead of scrolling smoothly.
func TestUpdateHistoryWindow_PersistsOffsetAcrossRenders(t *testing.T) {
	m := testUIModel()
	m.nav.histHeight = 5
	steps := make([]*stepRow, 20)
	for i := range steps {
		steps[i] = &stepRow{command: "cmd"}
	}
	m.blocks = []*goalBlock{{goal: "g", steps: steps}}
	m.nav.follow = false
	m.nav.cursor = 19 // last row, well past the 5-line window

	m.updateHistoryWindow()
	require.Greater(t, m.nav.histOffset, 0, "cursor past the window must scroll it forward, not stay pinned at 0")
	require.Len(t, m.nav.histWindow, m.nav.histHeight)
	assert.Contains(t, m.nav.histWindow[len(m.nav.histWindow)-1], "cmd", "cursor row must be visible in the window")

	// Re-render with nothing else changed: the offset a real pointer
	// receiver computed must still be there to read back — this is
	// exactly what the old value-receiver version could never do.
	offsetBefore := m.nav.histOffset
	m.updateHistoryWindow()
	assert.Equal(t, offsetBefore, m.nav.histOffset, "offset must persist across renders, not recompute from a stale zero")
}

// TestUI_StatusHintMatchesEscBehavior locks statusHint to onEscape's
// actual behavior: from the output pane, esc steps back to history only
// when nothing is running — with a command in flight it aborts instead
// (onEscape, keys.go), and the hint must say so rather than always
// advertising "history".
func TestUI_StatusHintMatchesEscBehavior(t *testing.T) {
	m := testUIModel()
	m.nav.focus = focusOutput
	assert.Contains(t, m.statusHint(), "[esc] history")
	assert.NotContains(t, m.statusHint(), "[esc] abort")

	_, cancel := context.WithCancel(context.Background())
	m.abort = cancel
	assert.Contains(t, m.statusHint(), "[esc] abort")
	assert.NotContains(t, m.statusHint(), "[esc] history")
}

func TestUI_TableRendersInDetailZone(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{tableRow()}}}
	m.nav.cursor = 0
	m.refreshViewport()
	v := m.View()
	assert.Contains(t, v, "USER")
	assert.Contains(t, v, "4821")
	assert.Contains(t, v, "table")
}

func TestUI_StyledBodyKinds(t *testing.T) {
	m := testUIModel()
	mk := func(kind, out string) *stepRow {
		return &stepRow{command: "cmd", cmd: cmdState{ec: &agent.ExecutedCommand{
			Result: shell.Result{Stdout: out},
			Post:   &agent.PostJudgment{RenderKind: kind},
		}}}
	}
	assert.Contains(t, m.styledBody(mk(agent.KindJSON, `{"b":2,"a":1}`)), "\n")
	assert.Contains(t, m.styledBody(mk(agent.KindContent, "package main\n")), "1")
	got := m.styledBody(mk(agent.KindDiff, "+a\n-b\n ctx\n"))
	assert.Contains(t, got, "+a")
	raw := m.styledBody(mk("unknown-kind", "plain\n"))
	assert.Equal(t, "plain", raw)
}

func TestUI_PaneMarkersFollowFocus(t *testing.T) {
	m := testUIModel()

	v := m.View()
	require.Contains(t, v, "○ history")
	require.Contains(t, v, "○ output")
	require.Contains(t, v, "● ❯", "input marker active on input focus")

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = nm.(Model)
	v = m.View()
	require.Contains(t, v, "● history")
	require.Contains(t, v, "○ output")
	require.Contains(t, v, "○ ❯")

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = nm.(Model)
	v = m.View()
	require.Contains(t, v, "○ history")
	require.Contains(t, v, "● output")
	require.Contains(t, v, "○ ❯")
}

func TestUI_IslandsRender(t *testing.T) {
	m := testUIModel()
	v := m.View()
	require.GreaterOrEqual(t, strings.Count(v, "╭"), 3, "history, output and input islands")
	lines := strings.Count(v, "\n") + 1
	require.Equal(t, m.layout.height, lines, "islands must tile the terminal exactly")
}

func TestUI_ExpandedRowsCannotPushOutSessionBar(t *testing.T) {
	m := testUIModel()
	steps := []*stepRow{}
	for range 30 {
		steps = append(steps, &stepRow{command: "cmd", cmd: cmdState{
			ec: &agent.ExecutedCommand{
				Result: shell.Result{Stdout: "out\n"},
				Post:   &agent.PostJudgment{RenderKind: agent.KindInline},
			},
			expanded: true,
		}})
	}
	m.blocks = []*goalBlock{{goal: "g", steps: steps, ended: true, end: agent.EndDone, summary: "s\nsecond line"}}
	m.nav.cursor = len(steps) - 1
	m.nav.follow = false
	v := m.View()
	lines := strings.Count(v, "\n") + 1
	require.Equal(t, m.layout.height, lines, "expanded rows and multiline banners must not grow the frame")
	require.Contains(t, v, "detent v2", "session bar stays on screen")
}

func TestUI_WideLinesCannotPushOutSessionBar(t *testing.T) {
	m := testUIModel()
	wide := strings.Repeat("w", 500)
	steps := []*stepRow{}
	for range 30 {
		steps = append(steps, &stepRow{command: "cmd " + wide, cmd: cmdState{ec: &agent.ExecutedCommand{
			Result: shell.Result{Stdout: wide + "\n"},
			Post:   &agent.PostJudgment{RenderKind: agent.KindLog},
		}}})
	}
	m.blocks = []*goalBlock{{goal: "g", steps: steps}}
	m.nav.cursor = 5
	m.nav.follow = false
	_ = m.View()
	m.nav.cursor = 25
	v := m.View()
	lines := strings.Count(v, "\n") + 1
	require.Equal(t, m.layout.height, lines, "wide content must not grow the frame")
	require.Contains(t, v, "detent v2", "session bar stays on screen")
	require.Contains(t, v, "…", "cuts marked visibly")
}
