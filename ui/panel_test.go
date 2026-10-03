package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// Every panel draws, at every size, with and without history.
func TestPanels_AllDraw(t *testing.T) {
	_, evs := oneTurn("go", "ls", "x\n")
	for _, k := range []panelKind{panelContext, panelStatus, panelHelp} {
		for _, with := range [][]event.Event{nil, evs} {
			m := sized(t, 100, 30, with...)
			m.panel.open = k
			lines := m.panelLines()
			require.NotEmpty(t, lines, "panel %v drew nothing", k)
			assert.NotEmpty(t, panelName(k))
		}
	}
}

// A share answers "how close am I", the raw pair answers "how much
// room is left". The page has space for both, the bar does not.
func TestStatusPage_ShowsContextBothWays(t *testing.T) {
	m := feed(t, event.SessionStarted{Model: "m", ContextTokens: 24_000})
	m.apply(event.StepEnded{Usage: event.Usage{PromptTokens: 16_800}})

	line := m.contextDetail()
	assert.Contains(t, line, "70%")
	assert.Contains(t, line, "16.8k")
	assert.Contains(t, line, "24.0k")
	assert.Contains(t, stripANSI(strings.Join(m.statusLines(), "\n")), "context")
}

func TestStatusPage_ContextSaysWhenThereIsNothingToShow(t *testing.T) {
	none := feed(t, event.SessionStarted{Model: "m"})
	assert.Contains(t, none.contextDetail(), "no budget")

	unmeasured := feed(t, event.SessionStarted{Model: "m", ContextTokens: 24_000})
	assert.Contains(t, unmeasured.contextDetail(), "nothing measured")
	assert.Contains(t, unmeasured.contextDetail(), "24.0k", "the budget is still worth saying")
}

// The page a human opens to find out why a tool is missing. Colour
// carries the state, so the state has to be right.
func TestMCPPage_ColoursEachServerByItsState(t *testing.T) {
	m := feed(t, event.SessionStarted{Model: "m"}, event.ServersListed{Servers: []event.ServerSummary{
		{Name: "github", Command: "docker", Connected: true, Tools: 26},
		{Name: "broken", Command: "/nope", Err: "no such file"},
		{Name: "archived", Command: "/notes", Disabled: true},
		{Name: "quiet", Command: "/quiet", Connected: true, Tools: 0},
		{Name: "pending", Command: "/slow"},
	}})
	page := strings.Join(m.mcpLines(), "\n")

	for _, want := range []string{"github", "26 tools", "broken", "no such file",
		"archived", "disabled", "quiet", "offers nothing", "pending", "connecting"} {
		assert.Contains(t, stripANSI(page), want)
	}

	// Each state gets its own colour, or the page says nothing a plain
	// list would not.
	assert.Contains(t, page, styleSafe.Render("●"), "a connected server is not marked safe")
	assert.Contains(t, page, styleDanger.Render("✗"), "a failed server is not marked dangerous")
	assert.Contains(t, page, styleFaint.Render("○"), "a disabled server is not marked faint")
	assert.Contains(t, page, styleCaution.Render("●"), "a server offering nothing is not marked caution")
	assert.Contains(t, page, styleFaint.Render("◌"), "a server still dialling is not marked faint")
}

func TestMCPPage_SaysWhenNothingIsConfigured(t *testing.T) {
	m := feed(t, event.SessionStarted{Model: "m"})
	assert.Contains(t, stripANSI(strings.Join(m.mcpLines(), "\n")), "none configured")
}

// Opening the page asks, since ui holds no servers of its own.
func TestSlashMCP_OpensThePageAndAsks(t *testing.T) {
	bus := event.New()
	seen, stop := bus.Subscribe(event.Only(event.ListServersKind))
	defer stop()

	m := New(t.Context(), bus, SessionInfo{})
	got, cmd := m.listServers("/mcp")
	assert.Equal(t, panelMCP, got.panel.open)

	require.NotNil(t, cmd)
	cmd()
	select {
	case rec := <-seen:
		assert.Equal(t, event.ListServersKind, rec.Event.Kind())
	case <-time.After(2 * time.Second):
		t.Fatal("opening /mcp asked nothing")
	}
}

// esc closes it, the way it closes every other page.
func TestMCPPage_EscapeCloses(t *testing.T) {
	m := feed(t, event.SessionStarted{Model: "m"})
	got, _ := m.listServers("/mcp")
	require.Equal(t, panelMCP, got.panel.open)

	closed, _ := got.onEscape()
	assert.Equal(t, panelNone, closed.panel.open)
}
