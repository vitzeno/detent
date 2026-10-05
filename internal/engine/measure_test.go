package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// History is split by request, in the order compaction folds it, and each
// request names what made it heavy.
func TestHistoryParts_SplitByRequestAndNameTheHeaviest(t *testing.T) {
	var tr transcript
	tr.user(1, "fix the tests")
	tr.step(model.Reply{Requests: []event.ToolRequest{bashCall("c1", "go test ./...")}},
		[]string{strings.Repeat("FAIL\n", 400)})
	tr.say("fixed")
	tr.user(2, "add grep")
	tr.step(model.Reply{Requests: []event.ToolRequest{readCall("c2", "grep.go")}}, []string{"short"})

	parts := historyParts(tr.msgs, tr.starts, tr.dropped, true)
	require.Len(t, parts, 2)
	assert.Equal(t, "fix the tests", parts[0].Name)
	assert.Equal(t, 1, parts[0].N)
	assert.Equal(t, "go test ./...", parts[0].Largest)
	assert.Empty(t, parts[0].Detail, "nothing was compacted, so nothing is partly summarised")
	assert.False(t, parts[0].Open)
	assert.Equal(t, "add grep", parts[1].Name)
	assert.True(t, parts[1].Open, "the last request is the running one")
	assert.Equal(t, tr.bytes(), parts[0].bytes+parts[1].bytes, "every message is in exactly one part")
}

// The first request starts at message 0, which is only the summary's
// place once compaction has put a note there.
func TestHistoryParts_TheOnlyRequestIsTheRunningOne(t *testing.T) {
	var tr transcript
	tr.user(1, "hello")
	parts := historyParts(tr.msgs, tr.starts, tr.dropped, true)
	require.Len(t, parts, 1)
	assert.Equal(t, event.ContextPart{Name: "hello", N: 1, Open: true}, parts[0].ContextPart)
}

func TestHistoryParts_TheSummarySaysWhatItReplaced(t *testing.T) {
	var tr transcript
	for n := 1; n <= 3; n++ {
		tr.user(n, "old request")
		tr.say(strings.Repeat("x", 4000))
	}
	tr.user(4, "now")
	_, note := tr.compact(context.Background(), 1500, stubSummarizer{out: "they did things"})
	require.NotEmpty(t, note)

	parts := historyParts(tr.msgs, tr.starts, tr.dropped, true)
	assert.Equal(t, "summary", parts[0].Name)
	assert.Equal(t, "requests 1 to 2", parts[0].Detail, "compaction folds only what it must")
	assert.Equal(t, 3, parts[1].N, "the third request survived whole")
	assert.Equal(t, "now", parts[len(parts)-1].Name)
	assert.True(t, parts[len(parts)-1].Open)
}

// The total is the endpoint's own count, and the parts are scaled to add
// up to it. With nothing new, asking again gets the same exact answer.
func TestMeasure_ScalesToTheRealCount(t *testing.T) {
	e := measured(t)
	got := e.measure(10_000)
	assert.True(t, got.Exact)
	assert.Equal(t, 10_000, got.Total)
	assert.InDelta(t, 10_000, sum(got.Fixed)+sum(got.History), 10, "the parts add up to the count")
	assert.Equal(t, got, e.measure(0), "nothing changed, so the exact answer stands")

	e.root.lock(func() { e.root.tr.note("a correction") })
	later := e.measure(0)
	assert.False(t, later.Exact, "something was added, so this is an estimate")
	assert.Greater(t, later.Total, got.Total)
}

func TestMeasure_FixedCostsAreGroupedByWhoOffersThem(t *testing.T) {
	e := measured(t)
	byName := map[string]event.ContextPart{}
	for _, p := range e.measure(0).Fixed {
		byName[p.Name] = p
	}
	assert.Equal(t, "8 built-in", byName["tools"].Detail)
	assert.Equal(t, "2 skills", byName["skills"].Detail)
	assert.Equal(t, "1 tool", byName["mcp · notion"].Detail)
	assert.Equal(t, "AGENTS.md", byName["instructions"].Detail)
	assert.Contains(t, byName, "detent")
}

// A tool says which row it belongs to, so a new kind of tool needs no
// change to measure.
func TestMeasure_ANewKindOfToolGetsItsOwnRow(t *testing.T) {
	e := measured(t)
	require.NoError(t, e.root.tools.Register(groupedTool{}))
	for _, p := range e.measure(0).Fixed {
		if p.Name == "plan" {
			assert.Equal(t, "a checklist", p.Detail)
			return
		}
	}
	t.Fatal("no plan row")
}

func TestGauge_GrowthIsTheAverageStepAndStartsOverAfterCompaction(t *testing.T) {
	var g gauge
	for _, h := range []int{100, 300, 500} {
		g.sample(h)
	}
	assert.Equal(t, 200, g.growth())
	g.sample(50)
	assert.Equal(t, 0, g.growth(), "history shrank, so the old rate says nothing")
}

func TestMeasure_PublishedAfterEveryStep(t *testing.T) {
	r := newRig(t, nil)
	r.run("hello")
	got := r.of(event.MeasuredKind)
	require.GreaterOrEqual(t, len(got), 2, "one at startup and one after the Step")
	last := got[len(got)-1].(event.ContextMeasured)
	assert.True(t, last.Exact)
	assert.Equal(t, 1, last.Total, "the fake endpoint counts one token")
}

// measured is an engine with a prompt, instructions, skills and an MCP
// tool, and one request in its transcript.
func measured(t *testing.T) *Engine {
	t.Helper()
	reg := tool.Standard(tool.NewSkill([]tool.SkillEntry{{Name: "a", Description: "x"}, {Name: "b", Description: "y"}}))
	require.NoError(t, reg.Register(mcpTool{}))
	e := New(event.New(), &sizedModel{}, reg, nil,
		WithInstructions([]string{"AGENTS.md"}),
		WithSkills([]event.SkillSummary{{Name: "a"}, {Name: "b"}}))
	defer e.unsub()
	e.root.lock(func() {
		e.root.tr.user(1, "do it")
		e.root.tr.say(strings.Repeat("y", 2000))
	})
	return e
}

type sizedModel struct{ fakeModel }

func (*sizedModel) PromptParts() []model.PromptPart {
	return []model.PromptPart{{Name: "detent", Bytes: 4000}, {Name: "instructions", Detail: "AGENTS.md", Bytes: 8000}}
}

type groupedTool struct{}

func (groupedTool) Name() string { return "update_plan" }
func (groupedTool) Describe() tool.Spec {
	return tool.Spec{Description: "keep a plan", Group: "plan", GroupDetail: "a checklist"}
}
func (groupedTool) Lower(tool.Args) (string, error) { return "", nil }

type mcpTool struct{}

func (mcpTool) Name() string { return "notion__search" }
func (mcpTool) Describe() tool.Spec {
	return tool.Spec{Description: "search notion", Executor: "notion", Group: "mcp · notion"}
}
func (mcpTool) Lower(tool.Args) (string, error) { return "", nil }
