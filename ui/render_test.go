package ui

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/google/uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// The whole screen must render at any size without panicking or
// spilling. 80 columns is the floor the session bar fits.
func TestView_RendersAtEverySize(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}, {200, 60}} {
		_, evs := oneTurn("go", "ls -la", "a\nb\nc\n")
		m := sized(t, size[0], size[1], evs...)
		out := m.baseView()
		require.NotEmpty(t, out)
		for l := range strings.SplitSeq(out, "\n") {
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

func TestTildePath_MatchesWholeElements(t *testing.T) {
	for _, c := range []struct{ dir, home, want string }{
		{"/Users/m/proj", "/Users/m", "~/proj"},
		{"/Users/m", "/Users/m", "~"},
		{"/Users/mx/proj", "/Users/m", "/Users/mx/proj"},
		{"/tmp", "", "/tmp"},
	} {
		assert.Equal(t, c.want, tildePath(c.dir, c.home), "%s under %s", c.dir, c.home)
	}
}

// The first screen a human sees must not send them to a command that
// does not exist, and welcome cannot see the registry to check.
func TestWelcome_OffersOnlyRealCommands(t *testing.T) {
	m := sized(t, 160, 60)
	pane := stripANSI(strings.Join(m.welcomeLines(), "\n"))
	_, after, found := strings.Cut(pane, "slash command")
	require.True(t, found, "the welcome pane names slash commands")
	cmds := regexp.MustCompile(`/[a-z]+`).FindAllString(after, -1)
	require.NotEmpty(t, cmds)
	for _, c := range cmds {
		_, ok := lookupSlash(c, nil)
		assert.True(t, ok, "%s is not a slash command", c)
	}
}

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
		event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": command}},
		event.ToolCallStarted{ToolCall: call, Runner: "host"},
		event.ToolCallEnded{ToolCall: call, Result: event.Result{Stdout: out}},
		event.TurnEnded{Turn: turn, Reason: event.EndDone},
	}
}
