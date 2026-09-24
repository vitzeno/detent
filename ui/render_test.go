package ui

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// sized builds a Model at a real terminal size with its panes laid
// out, so a render test measures what a human would see.
func sized(t *testing.T, w, h int, evs ...event.Event) Model {
	t.Helper()
	m := New(context.Background(), event.New(), SessionInfo{})
	m.layout.width, m.layout.height = w, h
	for _, e := range evs {
		m.apply(e)
	}
	m.sizeViewport()
	return m
}

func oneTurn(prompt, command, out string) (uuid.UUID, []event.Event) {
	turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	return turn, []event.Event{
		event.TurnStarted{Turn: turn, N: 1, Prompt: prompt},
		event.CallProposed{Call: call, Tool: "bash", Args: map[string]any{"command": command}},
		event.CallStarted{Call: call, Runner: "host"},
		event.CallEnded{Call: call, Result: event.Result{Stdout: out}},
		event.TurnEnded{Turn: turn, Reason: event.EndDone},
	}
}

// Nothing a block draws may run past the pane. The island truncates
// whatever overflows, so an overlong row loses its tail silently, and
// the rail gutter takes two columns every budget has to allow for.
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

// A multi-line command drew its tail outside the pane, over whatever
// was beside it. Seen with a python heredoc, which write_file now
// produces on every save.
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
	done, doneEvs := oneTurn("clean up", "ls", "ok\n")
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
	_ = done
}

// The whole screen must render at any size without panicking or
// spilling. 80 columns is the floor: the session bar has never fitted
// narrower than that, and this does not claim otherwise.
func TestView_RendersAtEverySize(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}, {200, 60}} {
		_, evs := oneTurn("go", "ls -la", "a\nb\nc\n")
		m := sized(t, size[0], size[1], evs...)
		out := m.baseView()
		require.NotEmpty(t, out)
		for _, l := range strings.Split(out, "\n") {
			assert.LessOrEqual(t, lipgloss.Width(stripANSI(l)), size[0],
				"a line overflows a %dx%d screen: %q", size[0], size[1], stripANSI(l))
		}
	}
}

func TestView_WelcomeShowsUntilSomethingHappens(t *testing.T) {
	m := sized(t, 120, 40)
	assert.True(t, m.showWelcome(), "nothing focused means the boot pane")

	_, evs := oneTurn("go", "ls", "x\n")
	m = sized(t, 120, 40, evs...)
	assert.False(t, m.showWelcome())
}

// Every panel draws, at every size, with and without history.
func TestPanels_AllDraw(t *testing.T) {
	_, evs := oneTurn("go", "ls", "x\n")
	for _, k := range []panelKind{panelUsage, panelStatus, panelHelp} {
		for _, with := range [][]event.Event{nil, evs} {
			m := sized(t, 100, 30, with...)
			m.panel.open = k
			lines := m.panelLines()
			require.NotEmpty(t, lines, "panel %v drew nothing", k)
			assert.NotEmpty(t, panelName(k))
		}
	}
}

// A call a human had to approve must not read like `ls` afterwards.
func TestHistory_FlaggedCallsAreMarked(t *testing.T) {
	turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m := sized(t, 120, 40,
		event.TurnStarted{Turn: turn, N: 1, Prompt: "clean"},
		event.CallProposed{Call: call, Tool: "bash", Args: map[string]any{"command": "rm -rf build"}},
		event.CallAssessed{Call: call, Risk: event.Risk{Dangerous: true, Note: "recursive delete"}},
		event.CallEnded{Call: call, Result: event.Result{}},
		event.TurnEnded{Turn: turn, Reason: event.EndDone})

	line := stripANSI(strings.Join(m.rowLines(m.rows()[0], m.focusedRow()), ""))
	assert.Contains(t, line, "!", "a flagged call carries a mark")

	// And an ordinary one does not.
	turn2, call2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m = sized(t, 120, 40,
		event.TurnStarted{Turn: turn2, N: 1, Prompt: "look"},
		event.CallProposed{Call: call2, Tool: "bash", Args: map[string]any{"command": "ls"}},
		event.CallEnded{Call: call2, Result: event.Result{}},
		event.TurnEnded{Turn: turn2, Reason: event.EndDone})
	assert.NotContains(t, stripANSI(strings.Join(m.rowLines(m.rows()[0], m.focusedRow()), "")), "!")
}

// A summary is prose, and the pane is where the whole of it is meant
// to be readable. Truncating there loses the tail to an ellipsis.
func TestSummary_WrapsInTheOutputPane(t *testing.T) {
	const said = "The build failed because the containerd socket was not reachable, " +
		"so every call fell back to the host shell and the snapshot was never taken."
	turn, evs := oneTurn("why did it fail", "make build", "boom\n")
	m := sized(t, 120, 40, append(evs[:len(evs)-1],
		event.ModelText{Turn: turn, Text: said},
		event.TurnEnded{Turn: turn, Reason: event.EndDone, Summary: said})...)

	require.Len(t, m.rows(), 2)
	m.nav.cursor = 1
	m.refreshViewport()

	inner := paneInner(m.layout.outputColW)
	body := stripANSI(m.viewContent)
	for i, l := range strings.Split(body, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(l), inner, "line %d overflows: %q", i, l)
	}
	assert.Contains(t, strings.Join(strings.Fields(body), " "), "snapshot was never taken",
		"the tail of the summary must survive")
	assert.Greater(t, len(strings.Split(body, "\n")), 1, "wrapped, not truncated to one line")

	for i, l := range strings.Split(m.baseView(), "\n") {
		assert.LessOrEqual(t, lipgloss.Width(stripANSI(l)), m.layout.width,
			"screen line %d overflows: %q", i, stripANSI(l))
	}
}

// Markdown, because the model writes markdown: a row shows the sentence
// with its markup stripped, and the pane draws the real thing.
func TestSummary_RendersAsMarkdownAndScrolls(t *testing.T) {
	said := "## What I found\n\n" +
		strings.Repeat("The containerd socket was not reachable, so every call fell back "+
			"to the host shell and no snapshot was taken. ", 40) +
		"\n\n- `colima status` says the VM is stopped\n"
	turn, evs := oneTurn("why did it fail", "make build", "boom\n")
	m := sized(t, 120, 40, append(evs[:len(evs)-1],
		event.ModelText{Turn: turn, Text: said},
		event.TurnEnded{Turn: turn, Reason: event.EndDone, Summary: said})...)

	m.nav.cursor = 1
	m.refreshViewport()
	body := stripANSI(m.viewContent)
	assert.NotContains(t, m.viewportHeader(), "—", "prose has no command, so no dangling dash")

	assert.Contains(t, body, "\u2022", "a list renders as a list, not as its source")
	assert.NotContains(t, body, "`colima status`", "inline code loses its backticks")

	require.Greater(t, len(strings.Split(body, "\n")), m.output.Height(),
		"the fixture must be taller than the pane for scrolling to mean anything")
	m.nav.focus = focusOutput
	m.output.ScrollDown(3)
	assert.Equal(t, 3, m.output.YOffset(), "the pane scrolls through the rest")
}

// The tail path must draw exactly what the full layout would have
// shown, or following renders something nothing else agrees with.
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

// A short session has to fall out of the tail loop rather than run off
// the front of the slice.
func TestHistoryWindow_TailHandlesLessThanOneScreen(t *testing.T) {
	m := session(1, 1, 0)
	m.nav.histHeight = 80
	m.nav.follow = true

	tail, _ := m.historyWindow()
	all, _ := m.historyAll()
	assert.Equal(t, all, tail)
}

// Leaving follow has to pin the offset, or the pane jumps to the top
// of the session the moment the cursor moves.
func TestNavUp_PinsTheOffsetOnLeavingFollow(t *testing.T) {
	m := session(21, 4, 12)
	m.nav.histHeight = 20
	m.nav.follow = true
	m.nav.cursor = len(m.rows()) - 1

	next, _ := m.navUp()
	got := next.(Model)
	require.False(t, got.nav.follow)

	all, _ := m.historyAll()
	assert.Equal(t, len(all)-20, got.nav.histOffset)
}

// uncached drops every block cache, so a test can compare what was
// drawn from cache against what a cold render produces.
func (m Model) uncached() Model {
	for _, b := range m.blocks {
		b.cache = nil
	}
	return m
}

// The one thing a cache can do wrong. Every mutation the UI supports
// is applied, and after each the cached drawing must equal a cold one.
func TestBlockCache_NeverGoesStale(t *testing.T) {
	m := session(6, 3, 8)
	rows := m.rows()
	turn := m.blocks[2].id
	call := m.blocks[2].rows[1].id

	steps := []struct {
		name string
		do   func(m *Model)
	}{
		{"cursor moves", func(m *Model) { m.nav.cursor = 4 }},
		{"cursor moves again", func(m *Model) { m.nav.cursor = 9 }},
		{"a row expands", func(m *Model) { m.toggleExpand(rows[4]) }},
		{"the same row shuts", func(m *Model) { m.toggleExpand(rows[4]) }},
		{"output arrives", func(m *Model) {
			m.apply(event.OutputChunk{Call: call, Line: "a new line of output"})
		}},
		{"a verdict lands", func(m *Model) {
			m.apply(event.CallJudged{Call: call, Status: "failed", Attention: 0.95, FromJudge: true})
		}},
		{"a turn ends", func(m *Model) {
			m.apply(event.TurnEnded{Turn: turn, Reason: event.EndError, Summary: "it broke"})
		}},
		{"a checkpoint lands", func(m *Model) { m.apply(event.CheckpointTaken{Turn: turn}) }},
		{"the pane narrows", func(m *Model) { m.layout.histColW = 44 }},
		{"a new turn starts", func(m *Model) {
			m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 7, Prompt: "one more"})
		}},
	}

	for _, s := range steps {
		s.do(&m)
		warm, warmCursor := m.historyAll()
		cold, coldCursor := m.uncached().historyAll()
		require.Equal(t, cold, warm, "after %s: the cache drew something a cold render would not", s.name)
		require.Equal(t, coldCursor, warmCursor, "after %s: cursor line diverged", s.name)
	}
}

// The cache exists to be hit. A test that only checked correctness
// would pass with caching switched off entirely.
func TestBlockCache_IsActuallyHit(t *testing.T) {
	m := session(40, 3, 8)
	m.nav.cursor = 60
	_, _ = m.historyAll()

	cached := 0
	for _, b := range m.blocks {
		if b.cache != nil {
			cached++
		}
	}
	require.Equal(t, len(m.blocks), cached, "every block should have been cached")

	// Move the cursor one row: only the block it left and the block it
	// joined may redraw.
	before := make([]*blockCache, len(m.blocks))
	for i, b := range m.blocks {
		before[i] = b.cache
	}
	m.nav.cursor = 61
	_, _ = m.historyAll()

	redrawn := 0
	for i, b := range m.blocks {
		if b.cache != before[i] {
			redrawn++
		}
	}
	assert.LessOrEqual(t, redrawn, 2, "a cursor move redrew %d blocks, not the two it touches", redrawn)
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

// Equivalence alone cannot see a cursor that never renders, because a
// cold draw would get it equally wrong. This asserts the mark exists,
// lands on one line, and moves when the cursor does.
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

// The live block redraws per spinner frame, or "thinking…" freezes
// while the request is still running.
func TestBlockCache_TheSpinnerStillTurns(t *testing.T) {
	m := session(3, 2, 0)
	turn := uuid.Must(uuid.NewV7())
	m.apply(event.TurnStarted{Turn: turn, N: 4, Prompt: "still working"})
	require.True(t, m.waiting)

	before, _ := m.historyAll()
	m.spinner, _ = m.spinner.Update(m.spinner.Tick())
	after, _ := m.historyAll()

	assert.NotEqual(t, before, after, "the cache pinned the spinner to one frame")
}

// cold recomputes the output pane from scratch, so a test can compare
// what the skip left on screen against what a forced redraw gives.
func (m Model) coldDetail() string {
	m.detail = detailKey{}
	m.refreshViewport()
	return m.viewContent
}

// The output pane must never show content the current state would not
// produce. Every input its key claims to cover is changed in turn, on
// the row the pane is actually showing and with columnar output, which
// is what the pane draws width-sensitively.
func TestDetailCache_NeverGoesStale(t *testing.T) {
	m := session(4, 3, 6)
	rows := m.rows()
	// Rows must draw differently, or moving the cursor between them
	// proves nothing about the key.
	m.apply(event.CallEnded{Call: rows[2].id, Result: event.Result{Stdout: "row two is its own thing\n"}})

	// A fresh Call, because a row binds its view once: feeding an
	// existing row a second result leaves it drawing the first.
	shown := uuid.Must(uuid.NewV7())
	m.apply(event.CallProposed{Call: shown, Tool: "bash", Args: map[string]any{"command": "df -h"}})
	m.nav.cursor = len(m.rows()) - 1
	m.layout.width, m.layout.height = 150, 45
	m.sizeViewport()

	table := "Filesystem      Size  Used Avail Capacity  Mounted on\n"
	for i := range 20 {
		table += fmt.Sprintf("/dev/disk%-3d    460Gi %dGi  %dGi    %d%%    /mnt/point%d\n", i, i*7, 400-i, i*3, i)
	}

	steps := []struct {
		name string
		do   func(m *Model)
	}{
		{"first render", func(m *Model) {}},
		{"the shown row streams output", func(m *Model) {
			m.apply(event.CallStarted{Call: shown, Runner: "sandbox"})
			m.apply(event.OutputChunk{Call: shown, Line: "a fresh line nobody has drawn yet"})
		}},
		{"the shown row finishes", func(m *Model) {
			m.apply(event.CallEnded{Call: shown, Result: event.Result{Stdout: table}})
		}},
		{"the focused row changes", func(m *Model) { m.nav.cursor = 2 }},
		{"and changes back", func(m *Model) { m.nav.cursor = len(m.rows()) - 1 }},
		{"the shown row expands", func(m *Model) { m.toggleExpand(m.focused()) }},
		{"usage opens", func(m *Model) { m.panel.open = panelUsage }},
		{"help opens", func(m *Model) { m.panel.open = panelHelp }},
		{"the panel closes", func(m *Model) { m.panel.open = panelNone }},
		// Through layout.width: sizeViewport derives the column widths,
		// so setting outputColW directly is overwritten immediately.
		{"the terminal narrows", func(m *Model) { m.layout.width = 74; m.sizeViewport() }},
		{"the terminal widens", func(m *Model) { m.layout.width = 190; m.sizeViewport() }},
		{"the terminal shortens", func(m *Model) { m.layout.height = 20; m.sizeViewport() }},
		{"focus moves to the pane", func(m *Model) { m.nav.focus = focusOutput }},
		{"the table cursor moves", func(m *Model) {
			if r := m.focused(); r != nil {
				r.tableCursor = 4
			}
		}},
		{"undo opens", func(m *Model) { m.mode = modeUndo }},
		{"undo closes", func(m *Model) { m.mode = modeInput }},
	}

	for _, s := range steps {
		s.do(&m)
		m.refreshViewport()
		require.Equal(t, m.coldDetail(), m.viewContent,
			"after %s: the pane kept content a fresh draw would not produce", s.name)
	}
}

// Scrolling must not redraw, or the skip is not doing its job.
func TestDetailCache_ScrollingDoesNotRedraw(t *testing.T) {
	m := session(4, 3, 6)
	m.nav.focus = focusOutput
	m.sizeViewport()
	m.refreshViewport()
	was := m.detail

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	got := next.(Model)
	assert.Equal(t, was, got.detail, "a scroll changed the key, so the content was redrawn")
}

// Whether the skip is safe is decided entirely by what the key
// covers, so this asserts coverage directly: change one input the
// pane draws from, and the key must change. Comparing rendered
// content cannot do this, because two inputs often draw the same.
func TestDetailKey_CoversEverythingThePaneDrawsFrom(t *testing.T) {
	inputs := []struct {
		name string
		do   func(m *Model)
	}{
		{"a fact arrives", func(m *Model) { m.apply(event.StepStarted{Turn: m.blocks[0].id, Step: uuid.Must(uuid.NewV7()), N: 9}) }},
		{"the terminal width", func(m *Model) { m.layout.width = 74; m.sizeViewport() }},
		{"the terminal height", func(m *Model) { m.layout.height = 20; m.sizeViewport() }},
		{"the focused row", func(m *Model) { m.nav.cursor = 1 }},
		{"a panel opens", func(m *Model) { m.panel.open = panelUsage }},
		{"the mode", func(m *Model) { m.mode = modeUndo }},
		{"which pane has focus", func(m *Model) { m.nav.focus = focusOutput }},
		{"the table cursor", func(m *Model) { m.focused().tableCursor = 4 }},
	}

	for _, in := range inputs {
		t.Run(in.name, func(t *testing.T) {
			m := session(3, 3, 4)
			m.nav.cursor, m.nav.focus = 2, focusHistory
			m.layout.width, m.layout.height = 150, 45
			m.sizeViewport()
			before := m.detailKey()

			in.do(&m)
			assert.NotEqual(t, before, m.detailKey(),
				"%s changed what the pane draws, but not the key it is cached under", in.name)
		})
	}
}
