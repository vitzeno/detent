package viewspec_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

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

func TestStat_DrawsLargeAndFallsBackWhenNarrow(t *testing.T) {
	spec := viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "stat", Title: "mounted", CountWhere: "capacity=45%", Of: "*"}}}

	got := draw(t, spec, dfOut, 40)
	require.Len(t, got, 4, "three rows of glyphs and a label")
	assert.Equal(t, "mounted", got[3])
	assert.Contains(t, strings.Join(got[:3], ""), "╱", "1/3 keeps its slash")

	narrow := draw(t, spec, dfOut, 6)
	require.Len(t, narrow, 1, "no room for a face, so it says the number plainly")
	assert.Contains(t, narrow[0], "1/3")
}

func TestFlow_FillsColumnsDownwards(t *testing.T) {
	const names = `Name
a.go
b.go
c.go
d.go
`
	spec := viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "flow", Field: "name", OnEnter: "vim {name}"}}}
	got := draw(t, spec, names, 14)

	require.Len(t, got, 2, "four names, two columns, two rows")
	assert.Equal(t, "a.go  c.go", got[0], "filled down the column, the way ls does")
	assert.Equal(t, "b.go  d.go", got[1])

	b := bind(t, spec, names)
	r, err := b.Draw(viewspec.Frame{Width: 14, Cursor: 3, Paint: viewspec.Plain()})
	require.NoError(t, err)
	assert.Equal(t, 1, r.CursorLine, "d.go is the second line of the second column")
	act, ok := b.Action(viewspec.Frame{Cursor: 3})
	require.True(t, ok)
	assert.Equal(t, "vim d.go", act)
}

func TestDots_NeedsTheAccentThatMakesItDots(t *testing.T) {
	const units = `Unit State
nginx running
redis failed
`
	spec := func(a *viewspec.Accent) viewspec.Spec {
		return viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
			{Kind: "dots", Field: "unit", Accent: a}}}
	}
	c, err := viewspec.Compile(spec(nil))
	require.NoError(t, err)
	_, err = c.Bind(units)
	require.Error(t, err, "without an accent it is only a list")

	got := draw(t, spec(&viewspec.Accent{Field: "state", Map: map[string]viewspec.Role{
		"running": viewspec.RoleSafe, "failed": viewspec.RoleDanger}}), units, 30)
	require.Len(t, got, 2)
	assert.True(t, strings.HasPrefix(got[0], "● nginx"))
	assert.Contains(t, got[1], "failed", "the state stays readable beside the glyph")
}

const dfOut = `Filesystem Size Used Capacity Mounted
disk1 926G 822G 95% /
disk2 926G 2.0G 45% /System
disk3 100G 50G 78% /Data
`

func cols(fields ...string) []viewspec.Column {
	out := make([]viewspec.Column, len(fields))
	for i, f := range fields {
		out[i] = viewspec.Column{Field: f}
	}
	return out
}

func colsParse() viewspec.Parse { return viewspec.Parse{Kind: "columns", Header: true} }

func fills(line string) int { return strings.Count(line, "█") }

func maxFill(lines []string) int {
	n := 0
	for _, l := range lines {
		n = max(n, fills(l))
	}
	return n
}
