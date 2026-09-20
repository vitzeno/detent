package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrapPlain(t *testing.T) {
	tests := []struct {
		name  string
		s     string
		width int
		want  []string
	}{
		{"fits on one line", "short", 20, []string{"short"}},
		{"wraps at a word boundary", "one two three four", 10, []string{"one two", "three four"}},
		{"width floors at 8", "abcdefghij", 1, []string{"abcdefgh", "ij"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, wrapPlain(tt.s, tt.width))
		})
	}
}

func TestHistoryLines_GoalWraps(t *testing.T) {
	m := testUIModel()
	m.layout.histColW = 40 // narrow enough to force a wrap
	m.blocks = []*goalBlock{{goal: "this goal is long enough that it must wrap across more than one physical line"}}

	lines := m.historyLines()
	require.Greater(t, len(lines), 1, "a long goal must produce more than one physical line")
	assert.Contains(t, lines[0], "goal ·")
	assert.True(t, strings.HasPrefix(lines[1], contPrefix), "continuation must be indented")
}

func TestHistoryLines_DividerBetweenBlocksOnly(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "first"}}
	assert.NotContains(t, strings.Join(m.historyLines(), "\n"), "─", "no divider before the first block")

	m.blocks = append(m.blocks, &goalBlock{goal: "second"})
	joined := strings.Join(m.historyLines(), "\n")
	assert.Contains(t, joined, "─", "a divider must separate two blocks")
}

func TestGoalBanner_SummaryWraps(t *testing.T) {
	m := testUIModel()
	m.layout.histColW = 40
	b := &goalBlock{
		ended: true, end: EndDone,
		summary: "a summary sentence long enough that it needs to wrap across two or more lines",
	}
	lines := m.goalBanner(b)
	require.Greater(t, len(lines), 1)
	assert.Contains(t, lines[0], "✔")
	assert.True(t, strings.HasPrefix(lines[1], contPrefix))
}

func TestStepLines_CommandStaysTruncatedNotWrapped(t *testing.T) {
	m := testUIModel()
	m.layout.histColW = 40
	row := &stepRow{command: strings.Repeat("verylongpathwithnospaces/", 5)}
	lines := m.stepLines(row)
	assert.Len(t, lines, 1, "a command with no spaces must truncate to one line, not fragment across several")
	assert.Contains(t, lines[0], "…")
}
