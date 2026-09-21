package ui

import (
	"charm.land/lipgloss/v2"
	"fmt"
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

// A long goal wraps rather than truncating: a human may be looking for
// exactly the part that would get cut. The rail carries on down the
// wrapped rows, which is what says they belong to the same goal.
func TestHistoryLines_GoalWraps(t *testing.T) {
	m := testUIModel()
	m.layout.histColW = 40 // narrow enough to force a wrap
	m.blocks = []*goalBlock{{goal: "this goal is long enough that it must wrap across more than one physical line"}}

	lines, _ := m.historyLines()
	require.Greater(t, len(lines), 1, "a long goal must produce more than one physical line")
	for i, l := range lines {
		assert.Contains(t, l, "┃", "row %d must stay inside the block's rail", i)
	}
	assert.Contains(t, stripANSI(lines[0]), "this goal is long")
}

// One blank row between blocks, never before the first: the rails
// already mark where each one reaches, so the gap is breathing room
// rather than the thing doing the separating.
func TestHistoryLines_BlankRowBetweenBlocksOnly(t *testing.T) {
	blanks := func(lines []string) int {
		n := 0
		for _, l := range lines {
			if strings.TrimSpace(stripANSI(l)) == "" {
				n++
			}
		}
		return n
	}
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "first"}}
	first, _ := m.historyLines()
	assert.Zero(t, blanks(first), "nothing separates a lone block from the top of the pane")

	m.blocks = append(m.blocks, &goalBlock{goal: "second"})
	both, _ := m.historyLines()
	assert.Equal(t, 1, blanks(both), "exactly one blank row between two blocks")
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
	assert.Contains(t, stripANSI(lines[0]), "a summary sentence")
	assert.True(t, strings.HasPrefix(lines[1], contPrefix),
		"continuation lines indent under the first")
}

func TestStepLines_CommandStaysTruncatedNotWrapped(t *testing.T) {
	m := testUIModel()
	m.layout.histColW = 40
	row := &stepRow{command: strings.Repeat("verylongpathwithnospaces/", 5)}
	lines := m.stepLines(row, 1)
	assert.Len(t, lines, 1, "a command with no spaces must truncate to one line, not fragment across several")
	assert.Contains(t, lines[0], "…")
}

func TestStepLines_ShowsCheckpointMarkerForSandboxedStep(t *testing.T) {
	m := testUIModel()
	row := &stepRow{command: "echo ok", cmd: cmdState{ec: &ExecutedCommand{SnapshotID: "snap-1"}}}
	lines := m.stepLines(row, 3)
	assert.Contains(t, lines[0], "#3", "a sandboxed step shows its rollback target")
}

func TestStepLines_NoMarkerWithoutSnapshot(t *testing.T) {
	m := testUIModel()
	row := &stepRow{command: "echo ok", cmd: cmdState{ec: &ExecutedCommand{}}}
	lines := m.stepLines(row, 3)
	assert.NotContains(t, lines[0], "#3", "a host-run step has no checkpoint to roll back to")
}

// Nothing a block draws may run past the pane. The island truncates
// whatever overflows, so an overlong row loses its tail silently —
// and the rail gutter takes two columns every budget has to allow for.
func TestHistory_RowsFitThePane(t *testing.T) {
	for _, width := range []int{120, 100, 80, 70} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := testUIModel()
			m.layout.width, m.layout.height = width, 40
			m.sizeViewport()
			m.blocks = []*goalBlock{railTestBlock()}
			m.nav.cursor = 0
			m.sizeViewport()

			inner := paneInner(m.layout.histColW)
			lines, _ := m.historyLines()
			for i, l := range lines {
				assert.LessOrEqual(t, lipgloss.Width(stripANSI(l)), inner,
					"row %d overflows the %d-wide pane: %q", i, inner, stripANSI(l))
			}
		})
	}
}

// The rail spans every row of its block and says how the goal went,
// so the grouping and the outcome both read from anywhere inside it —
// a divider only marked where two blocks met.
func TestHistory_RailSpansTheBlockAndCarriesOutcome(t *testing.T) {
	m := testUIModel()
	done := railTestBlock()
	aborted := &goalBlock{goal: "clean up", res: &GoalResult{Goal: "clean up"}, ended: true,
		end: EndAborted, steps: []*stepRow{{command: "rm -rf /tmp/x"}}}
	m.blocks = []*goalBlock{done, aborted}
	m.sizeViewport()

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
	assert.Equal(t, 1, blank, "one blank row separates the two blocks")

	// Outcome colour, not just presence.
	assert.Equal(t, styleSafe.Render("┃"), m.railStyle(done).Render("┃"), "a met goal rails green")
	assert.Equal(t, styleCaution.Render("┃"), m.railStyle(aborted).Render("┃"), "an aborted goal rails amber")

	// A goal still in flight takes the accent, so the live one stands out.
	running := &goalBlock{goal: "in flight", res: &GoalResult{Goal: "in flight"}}
	m.cur = running
	assert.Equal(t, styleRowCursor.Render("┃"), m.railStyle(running).Render("┃"))
}

func railTestBlock() *goalBlock {
	p := PostJudgment{FromJudge: true, Status: "clean_success", RenderKind: KindText, GoalAchieved: 0.95}
	b := &goalBlock{
		goal: "find the very large files somewhere under this project directory",
		res:  &GoalResult{Goal: "g"}, ended: true, end: EndDone,
		summary: "found two large files under experiments/runs",
		steps: []*stepRow{{command: strings.Repeat("verylongpath/", 6), cmd: cmdState{
			ec: &ExecutedCommand{Command: "du", Result: Result{Stdout: "x\n"}, SnapshotID: "s", Post: &p}}}},
	}
	b.judge = goalVerdict(b)
	return b
}
