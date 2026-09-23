package ui

import (
	"context"
	"github.com/google/uuid"
	"regexp"
	"strings"
	"testing"

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
	line := stripANSI(strings.Join(m.rowLines(rows[1]), "\n"))
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

	line := stripANSI(strings.Join(m.rowLines(m.rows()[0]), ""))
	assert.Contains(t, line, "!", "a flagged call carries a mark")

	// And an ordinary one does not.
	turn2, call2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m = sized(t, 120, 40,
		event.TurnStarted{Turn: turn2, N: 1, Prompt: "look"},
		event.CallProposed{Call: call2, Tool: "bash", Args: map[string]any{"command": "ls"}},
		event.CallEnded{Call: call2, Result: event.Result{}},
		event.TurnEnded{Turn: turn2, Reason: event.EndDone})
	assert.NotContains(t, stripANSI(strings.Join(m.rowLines(m.rows()[0]), "")), "!")
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
