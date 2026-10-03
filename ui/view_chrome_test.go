package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// The bar says how full the budget is, since that is what decides
// when a Turn stalls to compact. The breakdown lives in /context.
func TestSessionBar_ShowsContextAsAPercentageOfBudget(t *testing.T) {
	m := feed(t, event.SessionStarted{Model: "m", ContextTokens: 24_000})
	m.apply(event.StepEnded{Usage: event.Usage{PromptTokens: 12_000}})
	assert.Equal(t, "ctx 50%", m.contextGauge())

	m.apply(event.StepEnded{Usage: event.Usage{PromptTokens: 23_000}})
	assert.Equal(t, "ctx 95%", m.contextGauge())
}

// Compaction shrinks the transcript, so the old reading would
// overstate the budget until the next Step measures the new one.
func TestSessionBar_CompactionClearsTheStaleReading(t *testing.T) {
	m := feed(t, event.SessionStarted{Model: "m", ContextTokens: 24_000})
	m.apply(event.StepEnded{Usage: event.Usage{PromptTokens: 23_000}})
	require.Equal(t, "ctx 95%", m.contextGauge())

	m.apply(event.Compacted{Dropped: 21, Note: "summary"})
	assert.NotContains(t, m.contextGauge(), "95", "the pre-compaction reading survived")
	assert.Contains(t, m.notice.text, "21 messages")
}

// Without a budget there is nothing to be a percentage of, so the
// raw total is better than a made-up denominator.
func TestSessionBar_FallsBackToRawTokensWithNoBudget(t *testing.T) {
	m := feed(t, event.SessionStarted{Model: "m"})
	m.apply(event.StepEnded{Usage: event.Usage{PromptTokens: 12_000}})
	assert.Contains(t, m.contextGauge(), "tok")
}

// Crossing the budget costs a summariser round trip mid-Turn, so the
// gauge warns on the way up rather than reporting after the stall.
func TestSessionBar_ContextGaugeWarnsBeforeTheStall(t *testing.T) {
	for _, c := range []struct {
		prompt int
		want   lipgloss.Style
		name   string
	}{
		{4_000, styleFaint, "17%, nothing to say"},
		{18_500, styleCaution, "77%, getting close"},
		{22_000, styleDanger, "91%, about to stall"},
	} {
		m := feed(t, event.SessionStarted{Model: "m", ContextTokens: 24_000})
		m.apply(event.StepEnded{Usage: event.Usage{PromptTokens: c.prompt}})
		assert.Equal(t, c.want.Render("x"), m.contextStyle().Render("x"), c.name)
	}
}

// The bar sheds rather than wraps, by indices that move whenever a
// segment does. Brand, run mode and judge are what survives.
func TestSessionBar_ShedsTheGaugeBeforeWhatMatters(t *testing.T) {
	m := feed(t, event.SessionStarted{Model: "a-long-model-name-here", Judge: "jev-1", ContextTokens: 24_000})
	m.apply(event.StepEnded{Usage: event.Usage{PromptTokens: 16_800}})

	m.layout.width = 150
	wide := stripANSI(m.sessionBar())
	require.Contains(t, wide, "ctx 70%")
	require.Contains(t, wide, "a-long-model-name-here")

	m.layout.width = 60
	narrow := stripANSI(m.sessionBar())
	assert.NotContains(t, narrow, "ctx 70%", "the gauge should go before the model")
	assert.Contains(t, narrow, "detent", "the brand is not droppable")
	assert.Contains(t, narrow, "jev", "the judge is not droppable")
}

// Counts belong on /status, which has room to label them. The bar is
// for what a glance needs while something is running.
func TestSessionBar_LeavesCountsToTheStatusPage(t *testing.T) {
	m := feed(t, event.SessionStarted{Model: "m", ContextTokens: 24_000})
	m.layout.width = 150
	turn, evs := aTurn("go")
	for _, e := range evs {
		m.apply(e)
	}
	call := uuid.Must(uuid.NewV7())
	m.apply(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "ls"}})
	m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone})

	bar := stripANSI(m.sessionBar())
	assert.NotContains(t, bar, "request", "counts are duplicated from /status")
	assert.NotContains(t, bar, "call(s)")

	status := stripANSI(strings.Join(m.statusLines(), "\n"))
	assert.Contains(t, status, "requests")
	assert.Contains(t, status, "tool calls")
}
