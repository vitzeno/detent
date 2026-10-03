package viewspec_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

func TestBar_ScalesToTheLargestValue(t *testing.T) {
	spec := viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{{
		Kind: "bar", Columns: []viewspec.Column{{Field: "pkg"}, {Field: "secs"}},
		Sort: &viewspec.Sort{Field: "secs", Numeric: true, Desc: true}}}}
	got := draw(t, spec, goTest, 46)
	require.Len(t, got, 4)

	bars := make([]int, len(got))
	for i, l := range got {
		bars[i] = strings.Count(l, "█")
		assert.LessOrEqual(t, len([]rune(l)), 46, "never wider than the frame")
	}
	// 10.200, 9.500, 1.203, 0.412: descending, and the largest fills.
	assert.Greater(t, bars[0], bars[1])
	assert.Greater(t, bars[1], bars[2])
	assert.Greater(t, bars[2], bars[3])
	assert.InDelta(t, 9.5/10.2, float64(bars[1])/float64(bars[0]), 0.05,
		"bar length tracks the value")
}

// gauge exists because bar's relative scale flattens the distinction
// that matters when everything is nearly full.
func TestGauge_IsFixedWhereBarIsRelative(t *testing.T) {
	blocks := []viewspec.Block{{Kind: "gauge", Columns: cols("mounted", "capacity")}}
	got := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: blocks}, dfOut, 60)
	require.Len(t, got, 3)

	assert.Contains(t, got[0], "95%")
	assert.Contains(t, got[1], "45%")
	assert.InDelta(t, float64(fills(got[0]))/2, float64(fills(got[1])), 1.5,
		"45 reads as about half of 95 on a fixed scale")

	bar := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "bar", Columns: cols("mounted", "capacity")}}}, dfOut, 60)
	assert.Equal(t, fills(bar[0]), maxFill(bar), "bar scales the largest to full")
	assert.Less(t, fills(got[0]), maxFill(got)+1)
	assert.NotEqual(t, fills(bar[1]), fills(got[1]),
		"the same 45% draws differently under the two scales")
}

// histogram is the only widget that counts, which is the whole reason
// it exists: a spec cannot carry data, so aggregation has to be drawn.
func TestHistogram_CountsAndOrdersByCount(t *testing.T) {
	const ps = `USER PID COMMAND
mo 1 zsh
mo 2 go
root 3 launchd
mo 4 node
root 5 syslogd
_www 6 httpd
`
	got := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "histogram", Field: "user"}}}, ps, 40)

	require.Len(t, got, 3)
	assert.Contains(t, got[0], "mo")
	assert.True(t, strings.HasSuffix(strings.TrimRight(got[0], " "), "3"))
	assert.Greater(t, fills(got[0]), fills(got[1]), "biggest bar first")
	assert.Contains(t, got[2], "_www")
}

func TestStack_SegmentsFillTheWidthExactly(t *testing.T) {
	const width = 50
	got := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "stack", Columns: cols("mounted", "used")}}}, dfOut, width)

	require.Len(t, got, 2, "a band and its legend")
	assert.Equal(t, width, fills(got[0]), "rounding goes to the largest segment, never off the end")
	assert.Contains(t, got[1], "/System", "every segment is named")
}

func TestDiverge_DrawsBothWingsOffOneAxis(t *testing.T) {
	const numstat = `Added Removed Path
40 3 a.go
2 90 b.go
`
	got := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "diverge", Columns: cols("path", "removed", "added")}}}, numstat, 60)

	require.Len(t, got, 2)
	left, right, found := strings.Cut(got[0], "│")
	require.True(t, found, "a centre line separates the wings")
	assert.Less(t, fills(left), fills(right), "a.go removed 3 and added 40")

	left, right, _ = strings.Cut(got[1], "│")
	assert.Greater(t, fills(left), fills(right), "b.go is the other way round")
}

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

func TestSparkline_ScalesToTheValuesPresent(t *testing.T) {
	spec := viewspec.Spec{Parse: linesParse(),
		Blocks: []viewspec.Block{{Kind: "sparkline", Field: "secs", Title: "took"}}}
	got := draw(t, spec, goTest, 60)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "took ")
	assert.Contains(t, got[0], "0.412–10.2", "the range it scaled against")
}

func TestScatter_PlacesPointsInBrailleCells(t *testing.T) {
	const xy = `X Y
0 0
10 100
`
	got := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "scatter", Columns: cols("x", "y")}}}, xy, 20)

	require.Len(t, got, 9, "eight plot rows and an axis note")
	top, bottom := []rune(got[0]), []rune(got[7])
	assert.NotEqual(t, '⠀', top[len(top)-1], "the high point sits top right")
	assert.NotEqual(t, '⠀', bottom[0], "the low point sits bottom left")
	assert.Contains(t, got[8], "0..100")
}

func TestHeatmap_StacksMultiRuneColumnKeys(t *testing.T) {
	const commits = `Day Hour
Mon 09
Mon 10
Mon 10
Tue 22
`
	got := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "heatmap", Columns: cols("day", "hour")}}}, commits, 30)

	require.Len(t, got, 4, "two header rows and two days")
	// Cells take the spare width, so the axis pads to match them.
	assert.Equal(t, "0  1  2", strings.TrimRight(got[0][4:], " "), "the tens digit of 09, 10 and 22")
	assert.Equal(t, "9  0  2", strings.TrimRight(got[1][4:], " "), "and the units under it")
	assert.Contains(t, got[2], "Mon")
	assert.Equal(t, 3, strings.Count(got[2], "█"), "10 came twice: one cell three wide")
}

func TestHeatmap_CellsTakeTheSpareWidth(t *testing.T) {
	const commits = "Day Hour\nMon 09\nTue 10\n"
	narrow := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "heatmap", Columns: cols("day", "hour")}}}, commits, 8)
	wide := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "heatmap", Columns: cols("day", "hour")}}}, commits, 60)

	assert.Less(t, len(strings.TrimRight(narrow[2], " ")), len(strings.TrimRight(wide[2], " ")),
		"the same two columns fill more of a wider pane")
}

// A percentage, a size and a separated count are all things ParseFloat
// rejects, so a chart drawn from them would come out empty.
func TestNumber_ReadsWhatAShellPrints(t *testing.T) {
	const sizes = `Name Amount
kilo 900M
mega 1.0G
count 1,024
pct 64%
`
	// Sorting reads the numbers without rounding them to cells, so a
	// unit that failed to parse shows up as an order, not as a width.
	got := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "list", Field: "name", Sort: &viewspec.Sort{Field: "amount", Numeric: true, Desc: true}}}},
		sizes, 40)
	assert.Equal(t, []string{"mega", "kilo", "count", "pct"}, got,
		"1.0G over 900M over 1,024 over 64%")

	bars := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "bar", Columns: cols("name", "amount")}}}, sizes, 60)
	require.Len(t, bars, 4)
	assert.Positive(t, fills(bars[0]), "900M is not zero just because it carries a unit")
	assert.Greater(t, fills(bars[1]), fills(bars[0]), "and 1.0G outranks it")
}

const dfOut = `Filesystem Size Used Capacity Mounted
disk1 926G 822G 95% /
disk2 926G 2.0G 45% /System
disk3 100G 50G 78% /Data
`

const blameOut = `Unit Start Took
network 0 2
sshd 4 2
`

func fills(line string) int { return strings.Count(line, "█") }

func maxFill(lines []string) int {
	n := 0
	for _, l := range lines {
		n = max(n, fills(l))
	}
	return n
}

func firstFill(line string) int { return strings.IndexRune(line, '█') }
