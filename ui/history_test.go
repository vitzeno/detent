package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// Nothing a block draws may run past the pane, or the island silently
// cuts its tail. The rail gutter takes two columns of every budget.
func TestHistory_RowsFitThePane(t *testing.T) {
	for _, width := range []int{120, 100, 80, 70} {
		_, evs := oneTurn("find the very large files somewhere under this project directory",
			strings.Repeat("verylongpath/", 6), "x\n")
		m := sized(t, width, 40, evs...)

		inner := paneInner(m.layout.histColW)
		lines, _ := m.historyLines()
		for i, l := range lines {
			assert.LessOrEqual(t, lipgloss.Width(stripANSI(l)), inner,
				"width %d: row %d overflows the %d-wide pane: %q", width, i, inner, stripANSI(l))
		}
	}
}

// A multi-line command, such as the heredoc write_file produces, must
// not draw its tail outside the pane.
func TestHistory_MultiLineCommandStaysInsideThePane(t *testing.T) {
	const heredoc = "cat > 'x.py' <<'DETENT_EOF'\nimport re\np = 'ui/apply.go'\nDETENT_EOF"
	_, evs := oneTurn("rewrite the flow\nacross two lines", heredoc, "ok\n")
	m := sized(t, 120, 40, evs...)

	inner := paneInner(m.layout.histColW)
	lines, _ := m.historyLines()
	for i, l := range lines {
		assert.NotContains(t, l, "\n", "row %d is more than one line", i)
		assert.LessOrEqual(t, lipgloss.Width(stripANSI(l)), inner,
			"row %d overflows: %q", i, stripANSI(l))
	}
	m.nav.focus = focusOutput
	assert.NotContains(t, m.viewportHeader(), "\n")
}

// The model's closing words get a row of their own, so history shows
// them as one line to select rather than a paragraph in a banner.
func TestProse_IsItsOwnSelectableRow(t *testing.T) {
	const said = "a summary sentence long enough that it would have needed to wrap"
	turn, evs := oneTurn("g", "ls", "a\n")
	m := sized(t, 120, 40, append(evs[:len(evs)-1],
		event.ModelText{Turn: turn, Text: said},
		event.TurnEnded{Turn: turn, Reason: event.EndDone, Summary: said})...)

	rows := m.rows()
	require.Len(t, rows, 2, "the prose is a row like any other")
	assert.Equal(t, said, rows[1].prose)

	m.nav.cursor = 1
	line := stripANSI(strings.Join(m.rowLines(rows[1], m.focusedRow()), "\n"))
	assert.Contains(t, line, "a summary sentence")
	assert.NotContains(t, line, "✔", "no status badge: nothing ran")
	assert.Len(t, strings.Split(line, "\n"), 1, "one line, truncated, not wrapped")
}

// The rail spans every row of its block and carries the outcome, so
// grouping and result both read from anywhere inside it.
func TestHistory_RailSpansTheBlockAndCarriesOutcome(t *testing.T) {
	_, doneEvs := oneTurn("clean up", "ls", "ok\n")
	aborted := uuid.Must(uuid.NewV7())
	m := sized(t, 120, 40, append(doneEvs,
		event.TurnStarted{Turn: aborted, N: 2, Prompt: "second"},
		event.TurnEnded{Turn: aborted, Reason: event.EndAborted})...)

	lines, _ := m.historyLines()
	var railed, blank int
	for _, l := range lines {
		switch {
		case strings.Contains(l, "┃"):
			railed++
		case strings.TrimSpace(stripANSI(l)) == "":
			blank++
		}
	}
	assert.Equal(t, len(lines)-blank, railed, "every non-separator row carries a rail")
	assert.Equal(t, 1, blank, "one blank row between two blocks")

	assert.Equal(t, styleSafe.Render("┃"), m.railStyle(m.blocks[0]).Render("┃"), "a finished request rails green")
	assert.Equal(t, styleCaution.Render("┃"), m.railStyle(m.blocks[1]).Render("┃"), "an aborted one rails amber")
}

// A call a human had to approve must not read like `ls` afterwards.
func TestHistory_FlaggedToolCallsAreMarked(t *testing.T) {
	turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m := sized(t, 120, 40,
		event.TurnStarted{Turn: turn, N: 1, Prompt: "clean"},
		event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "rm -rf build"}},
		event.ToolCallAssessed{ToolCall: call, Risk: event.Risk{Dangerous: true, Note: "recursive delete"}},
		event.ToolCallEnded{ToolCall: call, Result: event.Result{}},
		event.TurnEnded{Turn: turn, Reason: event.EndDone})

	line := stripANSI(strings.Join(m.rowLines(m.rows()[0], m.focusedRow()), ""))
	assert.Contains(t, line, "!", "a flagged call carries a mark")

	// And an ordinary one does not.
	turn2, call2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m = sized(t, 120, 40,
		event.TurnStarted{Turn: turn2, N: 1, Prompt: "look"},
		event.ToolCallProposed{ToolCall: call2, Tool: "bash", Args: map[string]any{"command": "ls"}},
		event.ToolCallEnded{ToolCall: call2, Result: event.Result{}},
		event.TurnEnded{Turn: turn2, Reason: event.EndDone})
	assert.NotContains(t, stripANSI(strings.Join(m.rowLines(m.rows()[0], m.focusedRow()), "")), "!")
}

// Equivalence cannot see a cursor that never renders, since a cold
// draw gets it equally wrong. So assert the mark itself.
func TestHistory_CursorMarkFollowsTheCursor(t *testing.T) {
	m := session(6, 3, 0)
	m.nav.cursor = 2
	first := markedLine(m)
	require.NotEqual(t, -1, first, "the focused row carries no mark")

	m.nav.cursor = 11
	second := markedLine(m)
	require.NotEqual(t, -1, second, "the mark vanished when the cursor moved")
	assert.NotEqual(t, first, second, "the mark did not move with the cursor")

	lines, _ := m.historyAll()
	marks := 0
	for _, l := range lines {
		if strings.Contains(stripANSI(l), "▸") {
			marks++
		}
	}
	assert.Equal(t, 1, marks, "exactly one row is focused at a time")
}

// The tail must draw what the full layout would have shown.
func TestHistoryWindow_TailMatchesTheFullLayout(t *testing.T) {
	for _, turns := range []int{1, 3, 21, 50} {
		m := session(turns, 4, 12)
		m.nav.histHeight = 30
		m.nav.follow = true

		tail, offset := m.historyWindow()
		assert.Equal(t, -1, offset, "following never counts a total")

		all, _ := m.historyAll()
		want := all
		if len(want) > m.nav.histHeight {
			want = want[len(want)-m.nav.histHeight:]
		}
		assert.Equal(t, want, tail, "%d turns: tail diverged from the full layout", turns)
	}
}

// A short session falls out of the tail loop, not off the slice.
func TestHistoryWindow_TailHandlesLessThanOneScreen(t *testing.T) {
	m := session(1, 1, 0)
	m.nav.histHeight = 80
	m.nav.follow = true

	tail, _ := m.historyWindow()
	all, _ := m.historyAll()
	assert.Equal(t, all, tail)
}

// Leaving follow pins the offset, or the pane jumps to the top.
func TestNavUp_PinsTheOffsetOnLeavingFollow(t *testing.T) {
	m := session(21, 4, 12)
	m.nav.histHeight = 20
	m.nav.follow = true
	m.nav.cursor = len(m.rows()) - 1

	got, _ := m.navUp()
	require.False(t, got.nav.follow)

	all, _ := m.historyAll()
	assert.Equal(t, len(all)-20, got.nav.histOffset)
}

// Reading back through history, new tool calls must not pull the human away.
// end goes back to the newest and follows again, and so does a new request.
func TestHistory_NewRowsDoNotStealABrowsingCursor(t *testing.T) {
	turn, evs := aTurn("look around")
	call := func() event.Event {
		return event.ToolCallProposed{ToolCall: uuid.Must(uuid.NewV7()), Step: uuid.Must(uuid.NewV7()),
			Tool: "bash", Args: map[string]any{"command": "ls"}}
	}
	m := feed(t, append(evs, call(), call(), call())...)
	require.True(t, m.nav.follow, "a fresh session follows")

	m, _ = m.navUp()
	m, _ = m.navUp()
	browsing := m.nav.cursor
	require.False(t, m.nav.follow)

	m.apply(call())
	m.apply(call())
	assert.Equal(t, browsing, m.nav.cursor, "new calls left the cursor where the human put it")
	assert.False(t, m.nav.follow)

	m, _ = m.historyKey(tea.KeyPressMsg{Code: tea.KeyEnd})
	assert.True(t, m.nav.follow, "end follows again")
	assert.Equal(t, len(m.rows())-1, m.nav.cursor, "and lands on the newest")

	m, _ = m.navUp()
	m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone})
	m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 2, Prompt: "next"})
	assert.True(t, m.nav.follow, "a new request is the human's own, so it follows")
}

// markedLine is which rendered line carries the cursor mark, or -1.
func markedLine(m Model) int {
	lines, _ := m.historyAll()
	for i, l := range lines {
		if strings.Contains(stripANSI(l), "▸") {
			return i
		}
	}
	return -1
}

// A tool call leads with its tool's name, then what it touched, so a read or
// a search stands out from the commands around it.
func TestHistory_AToolCallLeadsWithItsName(t *testing.T) {
	for _, tt := range []struct {
		tool event.ToolName
		args map[string]any
		want string
	}{
		{"bash", map[string]any{"command": "go test ./..."}, "bash go test ./..."},
		{"read_file", map[string]any{"path": "ui/model.go", "offset": 40}, "read_file ui/model.go offset=40"},
		{"grep", map[string]any{"pattern": "TODO", "path": "."}, "grep TODO path=."},
		{"web_search", map[string]any{"query": "bubbletea v2"}, `web_search "bubbletea v2"`},
	} {
		turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		m := sized(t, 160, 30, event.TurnStarted{Turn: turn, N: 1, Prompt: "go"},
			event.ToolCallProposed{ToolCall: call, Tool: tt.tool, Args: tt.args})
		lines, _ := m.historyLines()
		assert.Contains(t, stripANSI(strings.Join(lines, "\n")), tt.want, tt.tool)
	}
}

// Each kind of tool has its own colour, and the human's own command none.
func TestToolStyle_ColoursByWhatAToolDoes(t *testing.T) {
	kind := func(tool event.ToolName, executor string) string {
		s, ok := toolStyle(&historyRow{tool: tool, executor: executor})
		require.True(t, ok)
		return s.Render("x")
	}
	seen := map[string]string{}
	for _, tt := range [][2]string{{"bash", ""}, {"read_file", ""}, {"edit_file", ""}, {"web_search", ""},
		{"linear__x", "mcp"}, {"skill", ""}} {
		got := kind(event.ToolName(tt[0]), tt[1])
		for other, was := range seen {
			assert.NotEqual(t, was, got, "%s and %s look the same", tt[0], other)
		}
		seen[tt[0]] = got
	}
	assert.Equal(t, kind("read_file", ""), kind("grep", ""), "reads share a colour")
	_, ok := toolStyle(&historyRow{human: true})
	assert.False(t, ok, "the human's own command is marked as theirs, not as a tool")
}

// A tool's name takes cells the command used to have, so rows must still fit.
func TestHistory_ToolRowsFitThePane(t *testing.T) {
	for _, width := range []int{120, 90, 70, 56} {
		turn := uuid.Must(uuid.NewV7())
		evs := []event.Event{event.TurnStarted{Turn: turn, N: 1, Prompt: "go"}}
		for _, tool := range []event.ToolName{"read_file", "linear__create_a_very_long_issue_name", "web_search"} {
			call := uuid.Must(uuid.NewV7())
			evs = append(evs, event.ToolCallProposed{ToolCall: call, Tool: tool,
				Args: map[string]any{"path": strings.Repeat("deep/", 20), "query": "x"}},
				event.ToolCallEnded{ToolCall: call, Result: event.Result{Stdout: "ok\n"}})
		}
		m := sized(t, width, 40, evs...)
		inner := paneInner(m.layout.histColW)
		lines, _ := m.historyLines()
		for i, l := range lines {
			assert.LessOrEqual(t, lipgloss.Width(l), inner, "width %d row %d: %q", width, i, stripANSI(l))
		}
	}
}
