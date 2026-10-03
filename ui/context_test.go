package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/vitzeno/detent/event"
)

func TestContextPage_SaysWhatFillsItAndWhy(t *testing.T) {
	m := measuredModel(t, event.New())
	got := stripANSI(strings.Join(m.contextLines(), "\n"))

	assert.Contains(t, got, "41.2k of 200k · 20%")
	assert.Contains(t, got, "fixed 20.4k, resent every step · history 20.8k, +2.1k a step")
	assert.Contains(t, got, "history compacts at 200k, about 85 steps away")
	assert.Contains(t, got, "44 tools, most of what is fixed", "the dominant fixed cost says so")
	assert.Contains(t, got, "requests 1 to 3")
	assert.Contains(t, got, "#4 fix the tests")
	assert.Contains(t, got, "go test ./... printed 6.2k", "a request names what made it heavy")
	assert.Contains(t, got, "#5 add grep")
	assert.NotContains(t, got, "grep -rn printed", "an item that is not much of its request is not named")
	assert.Contains(t, got, "running, kept until it ends")
	assert.NotContains(t, got, "estimated", "the endpoint counted this one")
	assert.Less(t, strings.Index(got, "summary"), strings.Index(got, "#4"), "oldest first")
}

func TestContextPage_SaysWhenNothingIsMeasured(t *testing.T) {
	m := New(t.Context(), event.New(), SessionInfo{})
	assert.Contains(t, stripANSI(strings.Join(m.contextLines(), "\n")), "nothing measured yet")
}

func TestContextBar_SplitsTheBudget(t *testing.T) {
	bar := stripANSI(contextBar(25, 25, 100, 20))
	assert.Equal(t, strings.Repeat("█", 10)+strings.Repeat("·", 10), bar)
	assert.Equal(t, strings.Repeat("█", 20), stripANSI(contextBar(90, 90, 100, 20)), "over budget fills, never overflows")
}

// Opened, the page asks for a fresh measurement, since a server may have
// connected since the last Step.
func TestContext_OpeningAsksForAMeasurement(t *testing.T) {
	bus := event.New()
	asked, unsub := bus.Subscribe(event.Only(event.MeasureContextKind))
	defer unsub()
	m := New(t.Context(), bus, SessionInfo{})

	next, cmd := m.runSlash("/context")
	runCmd(cmd)
	assert.Equal(t, panelContext, next.panel.open)
	select {
	case <-asked:
	case <-time.After(2 * time.Second):
		t.Fatal("/context asked for nothing")
	}
}

func measuredModel(t *testing.T, bus *event.Bus) Model {
	t.Helper()
	m := New(t.Context(), bus, SessionInfo{})
	m.layout.width, m.layout.height, m.layout.outputColW = 160, 40, 140
	m.apply(event.ContextMeasured{
		Budget: 200_000, Total: 41_200, Exact: true, Growth: 2_100,
		Fixed: []event.ContextPart{
			{Name: "mcp · notion", Detail: "44 tools", Tokens: 11_100},
			{Name: "instructions", Detail: "CLAUDE.md", Tokens: 7_200},
			{Name: "tools", Detail: "9 built-in", Tokens: 2_100},
		},
		History: []event.ContextPart{
			{Name: "summary", Detail: "requests 1 to 3", Tokens: 2_100},
			{Name: "fix the tests", N: 4, Tokens: 9_800, Largest: "go test ./...", LargestTokens: 6_200},
			{Name: "add grep", N: 5, Tokens: 6_900, Largest: "grep -rn TODO", LargestTokens: 1_000},
			{Name: "now", N: 6, Tokens: 2_000, Open: true},
		},
	})
	return m
}
