package viewspec_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

func TestGantt_PlacesEachBarWhereItStarted(t *testing.T) {
	got := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "gantt", Columns: cols("unit", "start", "took")}}}, blameOut, 40)

	require.Len(t, got, 2)
	assert.Less(t, firstFill(got[0]), firstFill(got[1]), "sshd starts after network")
	assert.Equal(t, strings.Count(got[0], "█"), strings.Count(got[1], "█"),
		"both ran the same length")
}

// instant is what lets an axis work on the shapes a shell prints,
// rather than only on numbers a spec could not have known.
func TestInstant_ReadsClocksDurationsAndNumbers(t *testing.T) {
	for _, tc := range []struct{ name, output string }{
		{"clock times", "Event At\nfirst 09:12\nlast 16:55\n"},
		{"go durations", "Event At\nfirst 120ms\nlast 4.5s\n"},
		{"plain numbers", "Event At\nfirst 3\nlast 900\n"},
		{"dates", "Event At\nfirst 2026-01-02\nlast 2026-09-21\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
				{Kind: "timeline", Columns: cols("event", "at")}}}, tc.output, 40)

			require.Len(t, got, 3, "an axis and two events")
			assert.Less(t, strings.IndexRune(got[1], '●'), strings.IndexRune(got[2], '●'),
				"the later event sits further along")
		})
	}

	// Unreadable is not zero: a column of words is not an axis.
	c, err := viewspec.Compile(viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "timeline", Columns: cols("event", "at")}}})
	require.NoError(t, err)
	b, err := c.Bind("Event At\nfirst never\nlast sometime\n")
	require.NoError(t, err)
	_, err = b.Draw(viewspec.Frame{Width: 40, Paint: viewspec.Plain()})
	assert.Error(t, err, "nothing readable on the axis fails the view rather than piling up at zero")
}

func TestBoxplot_BoxIsTheMiddleHalfAroundTheMedian(t *testing.T) {
	var b strings.Builder
	b.WriteString("Group Ms\n")
	for _, v := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"} {
		b.WriteString("api " + v + "\n")
	}
	got := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "boxplot", Columns: cols("group", "ms")}}}, b.String(), 50)

	require.Len(t, got, 1)
	line := got[0]
	median, box, whisker := strings.IndexRune(line, '█'), strings.IndexRune(line, '▒'), strings.IndexRune(line, '─')
	assert.Less(t, whisker, box, "the whisker reaches below the box")
	assert.Less(t, box, median, "and the median sits inside it")
	assert.Less(t, median, strings.LastIndex(line, "▒"), "with box on both sides")
	assert.Contains(t, line, "n=9")
}

func TestSeries_PutsEveryGroupOnOneScale(t *testing.T) {
	const out = `Host Ms
a 1
a 2
b 100
b 200
`
	got := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "series", Columns: cols("host", "ms")}}}, out, 40)

	require.Len(t, got, 3, "two strips and the scale they share")
	assert.Contains(t, got[0], "▁▁", "a is flat at the bottom of b's range")
	assert.Contains(t, got[1], "█", "b reaches the top")
	assert.Contains(t, got[2], "1..200 on one scale")
}

func TestDelta_WorksOutTheMoveWithoutJudgingIt(t *testing.T) {
	const bench = `Name Before After
up 100 150
down 100 50
same 100 100
`
	got := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "delta", Columns: cols("name", "before", "after")}}}, bench, 60)

	require.Len(t, got, 3)
	assert.Contains(t, got[0], "▲ +50 (+50%)")
	assert.Contains(t, got[1], "▼ -50 (-50%)")
	assert.Contains(t, got[2], "no change")

	// Direction carries no colour of its own: smaller is better for a
	// build and worse for coverage, and only a spec knows which.
	roles := drawPainted(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "delta", Columns: cols("name", "before", "after")}}}, bench, 60)
	assert.Equal(t, strings.Count(roles[0], "muted:"), strings.Count(roles[1], "muted:"),
		"up and down are painted alike")
}

func TestPanel_FramesOnePaneAndWillNotNest(t *testing.T) {
	spec := viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: viewspec.PanelKind, Title: "units", Panes: []viewspec.Pane{
			{Blocks: []viewspec.Block{{Kind: "list", Field: "unit", OnEnter: "systemctl status {unit}"}}},
		}}}}
	got := draw(t, spec, blameOut, 30)

	require.Len(t, got, 4, "a titled edge, two rows, and a bottom edge")
	assert.True(t, strings.HasPrefix(got[0], "╭─ units "), "the title sits in the top edge")
	for i, line := range got {
		assert.Len(t, []rune(line), 30, "line %d fills the frame", i)
	}
	assert.True(t, strings.HasPrefix(got[1], "│ ") && strings.HasSuffix(got[1], " │"))
	assert.True(t, strings.HasPrefix(got[3], "╰"))

	// The border costs a line, so a selection has to know about it.
	b := bind(t, spec, blameOut)
	r, err := b.Draw(viewspec.Frame{Width: 30, Cursor: 1, Paint: viewspec.Plain()})
	require.NoError(t, err)
	assert.Equal(t, 2, r.CursorLine, "row 1 sits under the top edge")

	for _, tc := range []struct {
		name  string
		block viewspec.Block
	}{
		{"two panes", viewspec.Block{Kind: viewspec.PanelKind, Panes: []viewspec.Pane{
			{Blocks: []viewspec.Block{{Kind: "log"}}}, {Blocks: []viewspec.Block{{Kind: "log"}}}}}},
		{"no panes", viewspec.Block{Kind: viewspec.PanelKind}},
		{"a row inside it", viewspec.Block{Kind: viewspec.PanelKind, Panes: []viewspec.Pane{
			{Blocks: []viewspec.Block{{Kind: viewspec.RowKind, Panes: []viewspec.Pane{
				{Blocks: []viewspec.Block{{Kind: "log"}}}, {Blocks: []viewspec.Block{{Kind: "log"}}}}}}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := viewspec.Compile(viewspec.Spec{Parse: colsParse(),
				Blocks: []viewspec.Block{tc.block}})
			assert.Error(t, err)
		})
	}
}

const blameOut = `Unit Start Took
network 0 2
sshd 4 2
`

func firstFill(line string) int { return strings.IndexRune(line, '█') }

func drawPainted(t *testing.T, spec viewspec.Spec, output string, width int) []string {
	t.Helper()
	r, err := bind(t, spec, output).Draw(viewspec.Frame{
		Width: width, Paint: rolePainter{viewspec.Plain()}})
	require.NoError(t, err)
	return r.Lines
}
