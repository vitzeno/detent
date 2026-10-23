package ui

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/google/uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/theme"
	"github.com/vitzeno/detent/ui/view"
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
	m = rested(m)

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
	m = rested(m)

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
		dir, home, want := filepath.FromSlash(c.dir), filepath.FromSlash(c.home), filepath.FromSlash(c.want)
		assert.Equal(t, want, tildePath(dir, home), "%s under %s", dir, home)
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

// A request's view leads history by its title, and the output pane, which
// follows onto it, draws its blocks rather than showing their YAML.
func TestRender_ARequestsViewIsDrawnInPlaceOfItsText(t *testing.T) {
	const final = "## Tests pass\n\n```chart\ntype: bar\ndata: {engine: 4, ui: 6}\n```\n"
	turn, evs := oneTurn("run the tests", "go test ./...", "ok\n")
	evs = append(evs[:len(evs)-1],
		event.ModelText{Turn: turn, Text: final},
		event.TurnEnded{Turn: turn, Reason: event.EndDone, Summary: final})
	m := rested(sized(t, 120, 40, evs...))

	history, _ := m.historyLines()
	assert.Contains(t, stripANSI(strings.Join(history, "\n")), "▤ Tests pass")
	pane := stripANSI(m.viewContent)
	assert.Contains(t, pane, "█", "the chart is drawn")
	assert.NotContains(t, pane, "type: bar", "not shown as code")
}

// A view that lands live arrives, a frame at a time, and then rests: the ticks
// stop, and what rests is the view drawn whole.
func TestReveal_ALiveViewArrivesThenRests(t *testing.T) {
	const final = "## Tests pass\n\n```chart\ntype: bar\ndata: {engine: 4, ui: 6}\n```\n"
	turn, evs := oneTurn("run the tests", "go test ./...", "ok\n")
	m := sized(t, 120, 40, append(evs[:len(evs)-1],
		event.ModelText{Turn: turn, Text: final},
		event.TurnEnded{Turn: turn, Reason: event.EndDone, Summary: final})...)

	require.NotNil(t, m.reveal.row, "a view landing live arrives")
	require.True(t, m.reveal.ticking, "and its first frame is on its way")
	assert.NotContains(t, stripANSI(m.viewContent), "ui     ██████", "it starts with nothing grown")

	frames := 0
	for m.reveal.row != nil {
		var cmd tea.Cmd
		m, cmd = m.update(revealMsg{})
		frames++
		require.LessOrEqual(t, frames, view.Frames, "an arrival must end")
		if m.reveal.row == nil {
			assert.Nil(t, cmd, "a view at rest asks for no more frames")
		} else {
			require.NotNil(t, cmd, "each frame asks for the next")
		}
	}
	m.sizeViewport()
	m.refreshViewport()
	assert.Equal(t, view.Render(final, theme.Current(), paneInner(m.layout.outputColW)), m.viewContent)
	history, _ := m.historyLines()
	assert.Contains(t, stripANSI(strings.Join(history, "\n")), "▤ Tests pass")
}

// Resuming a session must not replay its views arriving: they are history.
func TestReveal_AReplayedViewIsDrawnAtRest(t *testing.T) {
	const final = "## Tests pass\n"
	turn, evs := oneTurn("run the tests", "go test ./...", "ok\n")
	evs = append(evs[:len(evs)-1],
		event.ModelText{Turn: turn, Text: final},
		event.TurnEnded{Turn: turn, Reason: event.EndDone, Summary: final})
	records := make([]event.Record, len(evs))
	for i, e := range evs {
		records[i] = event.Record{Ordinal: uint64(i + 1), Event: e}
	}
	m := New(t.Context(), event.New(), SessionInfo{}).Restore(records)
	assert.Nil(t, m.reveal.row)
}

// A command's output is drawn as it came, except a diff, coloured as /review colours
// one: by language, with what it added and removed tinted the pane's width.
func TestRender_ADiffInAnOutputIsColouredAsTheReviewColoursIt(t *testing.T) {
	const diff = "--- a/x.go\n+++ b/x.go\n@@ -1,2 +1,2 @@\n-- old comment\n+func new() {}\n same\n"
	turn, evs := oneTurn("show the change", "make show", diff)
	m := sized(t, 120, 40, append(evs, event.TurnEnded{Turn: turn, Reason: event.EndDone})...)
	r := m.rows()[0]

	lines := m.rowBody(r, 80)
	require.Len(t, lines, 6, "two headers, the hunk header and its three lines")
	added, removed := sgr(48, palette.DiffAdded), sgr(48, palette.DiffRemoved)
	assert.Contains(t, lines[3], removed, "a removed line reading -- stays in the hunk")
	assert.Contains(t, lines[4], added)
	assert.NotContains(t, lines[5], "\x1b[48", "context is untinted")
	assert.Contains(t, stripANSI(m.viewportHeader()), "diff")

	_, evs = oneTurn("list", "ls", "a.go\nb.go\n")
	m = sized(t, 120, 40, evs...)
	assert.Equal(t, []string{"a.go", "b.go", ""}, m.rowBody(m.rows()[0], 80), "anything else is the text it is")
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// sized builds a Model at a real terminal size with its panes laid
// out, so a render test measures what a human would see.
func sized(t *testing.T, w, h int, evs ...event.Event) Model {
	t.Helper()
	m := New(t.Context(), event.New(), SessionInfo{})
	m.layout.width, m.layout.height = w, h
	for _, e := range evs {
		m.apply(e)
	}
	m.sizeViewport()
	return m
}

// rested lets a view that landed live finish arriving, as its ticks would.
func rested(m Model) Model {
	for m.reveal.row != nil {
		m, _ = m.update(revealMsg{})
	}
	m.sizeViewport()
	m.refreshViewport()
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

// Output, a page or a skill reads whole in the output pane: a line wider than
// the pane wraps, and no word of it is lost off the edge.
func TestOutputPane_WrapsWhatIsWiderThanIt(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("follow these steps closely ", 12))
	turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m := sized(t, 100, 40,
		event.TurnStarted{Turn: turn, N: 1, Prompt: "go"},
		event.ToolCallProposed{ToolCall: call, Tool: "skill", Args: map[string]any{"name": "x"}},
		event.ToolCallEnded{ToolCall: call, Result: event.Result{Stdout: long + "\n"}})
	width := m.output.Width()
	var words []string
	for l := range strings.SplitSeq(m.viewContent, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(l), width, "%q runs past the pane", stripANSI(l))
		words = append(words, strings.Fields(stripANSI(l))...)
	}
	assert.Equal(t, strings.Fields(long), words, "every word is on screen")
}

// A skill's description on /skills is a sentence, shown whole.
func TestSkillsPage_WrapsALongDescription(t *testing.T) {
	desc := strings.TrimSpace(strings.Repeat("use this when the request is about releases ", 6))
	m := sized(t, 90, 40, event.SessionStarted{Skills: []event.SkillSummary{
		{Name: "release", Description: desc, UserInvocable: true}}})
	got := stripANSI(strings.Join(m.skillLines(), "\n"))
	assert.Equal(t, strings.Fields(desc), strings.Fields(strings.SplitN(got, "/release", 2)[1]))
}
