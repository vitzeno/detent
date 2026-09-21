package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for view.go: rendering, sizing, and the detail-zone component
// dispatch. render_view.go's own transforms (errorSeverity, diffClass,
// prettyJSON) have their own render_view_test.go.

// TestHistoryWindow_PersistsOffsetAcrossRenders locks a bug where the
// windowing math lived in a value-receiver method reached only from
// View()'s call chain — Bubble Tea renders View() on a throwaway copy
// of Model, so writes to the offset never stuck. It is now pure and
// hands the offset back for Update to keep.
func TestHistoryWindow_PersistsOffsetAcrossRenders(t *testing.T) {
	m := testUIModel()
	m.nav.histHeight = 5
	steps := make([]*stepRow, 20)
	for i := range steps {
		steps[i] = &stepRow{command: "cmd"}
	}
	m.blocks = []*goalBlock{{goal: "g", steps: steps}}
	m.nav.follow = false
	m.nav.cursor = 19 // last row, well past the 5-line window

	window, offset := m.historyWindow()
	require.Greater(t, offset, 0, "cursor past the window must scroll it forward, not stay pinned at 0")
	require.Len(t, window, m.nav.histHeight)
	assert.Contains(t, window[len(window)-1], "cmd", "cursor row must be visible in the window")

	// Rendering must not move it: the function is pure, so the same
	// state gives the same offset however many times View runs.
	m.nav.histOffset = offset
	_ = m.View().Content
	_, again := m.historyWindow()
	assert.Equal(t, offset, again, "offset must be stable across renders")
}

// TestUI_StatusHintMatchesEscBehavior: from the output pane, esc steps
// back to history only when nothing is running — with a command in
// flight it aborts, and the hint must say so.
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
	v := m.View().Content
	assert.Contains(t, v, "USER")
	assert.Contains(t, v, "4821")
	assert.Contains(t, v, "table")
}

// The nine kinds were always specs; they used to be a switch. Each one
// now picks a built-in spec, so the pane has one render path.
func TestUI_RenderKindChoosesAFallbackSpec(t *testing.T) {
	tests := []struct {
		name   string
		kind   RenderKind
		stdout string
		want   []string
	}{
		{"json indents", KindJSON, `{"b":2,"a":1}`,
			[]string{"{", `  "a": 1,`, `  "b": 2`, "}"}},
		{"content is numbered", KindContent, "package main\n",
			[]string{"   1 package main"}},
		{"diff keeps its markers", KindDiff, "+a\n-b\n ctx\n",
			[]string{"+a", "-b", " ctx"}},
		{"an unknown kind shows the bytes", "unknown-kind", "plain\n",
			[]string{"plain"}},
		{"nothing judged yet shows the bytes", "", "plain\n",
			[]string{"plain"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, plainLines(rowForKind("cmd", tc.stdout, tc.kind)))
		})
	}
}

func TestUI_SessionBarShowsRunMode(t *testing.T) {
	sandboxed := New(context.Background(), newFakeDriver(), SessionInfo{Proposer: "m", RunMode: "sandbox"})
	sandboxed.layout.width, sandboxed.layout.height = 120, 40
	sandboxed.sizeViewport()
	require.Contains(t, sandboxed.View().Content, "sandbox")

	onHost := New(context.Background(), newFakeDriver(), SessionInfo{Proposer: "m", RunMode: "host"})
	onHost.layout.width, onHost.layout.height = 120, 40
	onHost.sizeViewport()
	v := onHost.View().Content
	require.Contains(t, v, "host")
	require.Contains(t, v, "unsandboxed", "host mode must say so, not just omit the sandbox badge")
}

func TestUI_PaneMarkersFollowFocus(t *testing.T) {
	m := testUIModel()
	// Nothing has run, so the output pane is titled "detent" (the
	// welcome state); the markers are what this test is about.
	v := plain(m.View().Content)
	require.Contains(t, v, "○ history")
	require.Contains(t, v, "○ detent")
	require.Contains(t, v, "● ❯", "input marker active on input focus")

	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = nm.(Model)
	v = plain(m.View().Content)
	require.Contains(t, v, "● history")
	require.Contains(t, v, "○ detent")
	require.Contains(t, v, "○ ❯")

	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = nm.(Model)
	v = plain(m.View().Content)
	require.Contains(t, v, "○ history")
	require.Contains(t, v, "● detent")
	require.Contains(t, v, "○ ❯")
}

func TestUI_IslandsRender(t *testing.T) {
	m := testUIModel()
	v := m.View().Content
	require.GreaterOrEqual(t, strings.Count(v, "╭"), 3, "history, output and input islands")
	lines := strings.Count(v, "\n") + 1
	require.Equal(t, m.layout.height, lines, "islands must tile the terminal exactly")
}

func TestUI_ExpandedRowsCannotPushOutSessionBar(t *testing.T) {
	m := testUIModel()
	steps := []*stepRow{}
	for range 30 {
		steps = append(steps, &stepRow{command: "cmd", cmd: cmdState{
			ec: &ExecutedCommand{
				Result: Result{Stdout: "out\n"},
				Post:   &PostJudgment{RenderKind: KindInline},
			},
			expanded: true,
		}})
	}
	m.blocks = []*goalBlock{{goal: "g", steps: steps, ended: true, end: EndDone, summary: "s\nsecond line"}}
	m.nav.cursor = len(steps) - 1
	m.nav.follow = false
	v := m.View().Content
	lines := strings.Count(v, "\n") + 1
	require.Equal(t, m.layout.height, lines, "expanded rows and multiline banners must not grow the frame")
	require.Contains(t, v, "detent v2", "session bar stays on screen")
}

func TestUI_WideLinesCannotPushOutSessionBar(t *testing.T) {
	m := testUIModel()
	wide := strings.Repeat("w", 500)
	steps := []*stepRow{}
	for range 30 {
		steps = append(steps, &stepRow{command: "cmd " + wide, cmd: cmdState{ec: &ExecutedCommand{
			Result: Result{Stdout: wide + "\n"},
			Post:   &PostJudgment{RenderKind: KindLog},
		}}})
	}
	m.blocks = []*goalBlock{{goal: "g", steps: steps}}
	m.nav.cursor = 5
	m.nav.follow = false
	_ = m.View().Content
	m.nav.cursor = 25
	v := m.View().Content
	lines := strings.Count(v, "\n") + 1
	require.Equal(t, m.layout.height, lines, "wide content must not grow the frame")
	require.Contains(t, v, "detent v2", "session bar stays on screen")
	require.Contains(t, v, "…", "cuts marked visibly")
}

// TestUI_ThinkingHintOffersAbort: starting a goal blurs the input and
// moves focus to history, so the history hint is the only thing a
// waiting human sees. esc has always aborted — the hint just never
// said so, which read as "you can't abort a thinking model".
func TestUI_ThinkingHintOffersAbort(t *testing.T) {
	m := testUIModel()
	require.NotContains(t, m.statusHint(), "abort", "nothing running yet")

	m.prompt.SetValue("find big files")
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)

	require.True(t, m.waiting)
	require.NotNil(t, m.abort, "a goal in flight must be cancellable")
	require.Contains(t, m.statusHint(), "[esc] abort")

	// And esc really does reach the cancel func.
	aborted := false
	m.abort = func() { aborted = true }
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.True(t, aborted)
	require.Nil(t, nm.(Model).abort)
}

// Model-written prose is dimmed and unmarked. Green means "this
// succeeded", and a whole generated sentence in it reads as though the
// sentence is the status — while the step rows above and the jev line
// below already say how it went.
func TestUI_ModelProseIsDimmedNotSignalled(t *testing.T) {
	m := themedModel()
	var summary string
	for _, l := range strings.Split(m.View().Content, "\n") {
		if strings.Contains(stripANSI(l), "did the thing") {
			summary = l
		}
	}
	require.NotEmpty(t, summary, "the goal summary must render")

	prose := ansiFor(t, summary, "did the thing")
	assert.Contains(t, prose, "38;2;137;146;168", "the prose is muted, not a status colour")
	assert.NotContains(t, stripANSI(summary), "✔", "no tick on the prose")
}

// ansiFor returns sub together with the escape immediately before it.
func ansiFor(t *testing.T, line, sub string) string {
	t.Helper()
	i := strings.Index(line, sub)
	require.GreaterOrEqual(t, i, 0, "%q not in %q", sub, stripANSI(line))
	start := strings.LastIndex(line[:i], "\x1b[")
	require.GreaterOrEqual(t, start, 0, "no styling before %q", sub)
	end := i + len(sub)
	if e := strings.Index(line[end:], "\x1b["); e >= 0 {
		end += e + len("\x1b[m")
	}
	return line[start:min(end, len(line))]
}
