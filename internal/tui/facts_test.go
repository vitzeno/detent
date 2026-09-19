package tui

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vitzeno/detent/internal/reduce"
)

// These use reduce's real reducers to build Facts, not hand-typed maps —
// the original bug (summarizeFacts asserting []any against reduce's
// actual []map[string]any / []string) only showed up against real
// reducer output, never against a fixture built to the wrong shape by hand.

func TestSummarizeFacts_ProcessList_RealShape(t *testing.T) {
	result := reduce.ProcessLines(" 4821 alice   node\n 5140 root    launchd\n")
	got := summarizeFacts(result.Facts)
	assert.Contains(t, got, "2 processes")
	assert.Contains(t, got, "pid 4821")
	assert.Contains(t, got, "node")
	assert.NotContains(t, got, "items", "must not fall through to the generic count-only fallback")
}

func TestSummarizeFacts_ProcessList_Truncated(t *testing.T) {
	var out string
	for i := range 60 {
		out += " " + strconv.Itoa(1000+i) + " alice proc\n"
	}
	result := reduce.ProcessLines(out)
	got := summarizeFacts(result.Facts)
	assert.Contains(t, got, "of 60 processes", "should surface total_found, not just the visible/capped count")
}

func TestSummarizeFacts_PathList_RealShape(t *testing.T) {
	result := reduce.PathLines("go.mod\ngo.sum\ninternal/\n")
	got := summarizeFacts(result.Facts)
	assert.Contains(t, got, "3 items")
	assert.Contains(t, got, "go.mod")
}

func TestSummarizeFacts_TextSummary_RealShape(t *testing.T) {
	result := reduce.TextSummary("module detent\n\ngo 1.26.1\n")
	got := summarizeFacts(result.Facts)
	assert.Contains(t, got, "module detent")
}

func TestSummarizeFacts_Nil_DoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() { summarizeFacts(nil) })
}

func TestRenderFacts_NeverProducesGoMapSyntax(t *testing.T) {
	result := reduce.ProcessLines(" 4821 alice   node\n 5140 root    launchd\n")
	got := renderFacts(result.Facts, "  ")
	assert.NotContains(t, got, "map[", "raw Go map syntax is exactly the bug being fixed")
	assert.Contains(t, got, "pid 4821")
	assert.Contains(t, got, "alice")
}

func TestRenderFacts_CapsLongListsWithMoreCount(t *testing.T) {
	var out string
	for i := range 20 {
		out += " " + strconv.Itoa(2000+i) + " alice proc\n"
	}
	result := reduce.ProcessLines(out)
	got := renderFacts(result.Facts, "  ")
	assert.Contains(t, got, "+12 more", "20 rows, maxFactRows=8 shown, 12 remaining")
}

func TestRenderFacts_UnknownShape_FallsBackToJSON_NotGoSyntax(t *testing.T) {
	got := renderFacts(map[string]any{"weird": []int{1, 2, 3}, "nested": map[string]any{"a": 1}}, "  ")
	assert.NotContains(t, got, "map[")
	assert.Contains(t, got, "\"weird\"")
}
