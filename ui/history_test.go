package ui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

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

// The model's closing words get a row of their own, so history shows
// them as one line to select rather than a paragraph wrapped into a
// banner nobody can scroll.
func TestProse_IsItsOwnSelectableRow(t *testing.T) {
	const said = "a summary sentence long enough that it would have needed to wrap"
	m := testUIModel()
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	b := &goalBlock{goal: "g", ended: true, end: EndDone, summary: said,
		steps: []*stepRow{{command: "ls"}, {prose: said}}}
	m.blocks = []*goalBlock{b}

	rows := m.rows()
	require.Len(t, rows, 2, "the prose is a row like any other")
	assert.Equal(t, said, rows[1].prose)

	m.nav.cursor = 1
	line := stripANSI(strings.Join(m.stepLines(rows[1], 1), "\n"))
	assert.Contains(t, line, "a summary sentence", "and it says what was said")
	assert.NotContains(t, line, "✔", "no status badge: nothing ran")
	assert.Len(t, strings.Split(line, "\n"), 1, "one line, truncated, not wrapped")

	// The banner stops repeating it.
	assert.NotContains(t, stripANSI(strings.Join(m.goalBanner(b), "\n")), "a summary sentence")
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

// A multi-line command drew its tail outside the pane, over whatever
// was beside it: continuation lines carried no rail, so the block's
// left edge vanished and the rows ran into the output pane. Seen with
// a python heredoc, which is a perfectly ordinary thing to run.
func TestHistory_MultiLineCommandStaysInsideThePane(t *testing.T) {
	const heredoc = "python3 - <<'EOF'\nimport re\np = 'ui/goal_flow.go'\ns = open(p).read()\nEOF"

	m := testUIModel()
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	b := railTestBlock()
	b.goal = "rewrite the flow\nacross two lines"
	b.steps[0].command = heredoc
	m.blocks = []*goalBlock{b}
	m.nav.cursor = 0
	m.sizeViewport()

	inner := paneInner(m.layout.histColW)
	lines, _ := m.historyLines()
	for i, l := range lines {
		assert.NotContains(t, l, "\n", "row %d is more than one line", i)
		assert.LessOrEqual(t, lipgloss.Width(stripANSI(l)), inner,
			"row %d overflows the %d-wide pane: %q", i, inner, stripANSI(l))
	}

	// And the pane header, which sits inside its own border.
	m.nav.focus = focusOutput
	assert.NotContains(t, m.viewportHeader(), "\n")
}
