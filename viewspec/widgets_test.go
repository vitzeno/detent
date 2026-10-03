package viewspec_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

// A table naming no columns draws every parsed field, which is what
// lets one spec serve output whose columns are unknown until parsed.
func TestTable_WithoutColumnsUsesTheParseOrder(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "columns", Header: true},
		Blocks: []viewspec.Block{{Kind: "table"}}}
	got := draw(t, spec, "ZEBRA  ALPHA  MIDDLE\n1      2      3\n", 40)
	require.Len(t, got, 2)
	assert.Equal(t, "ZEBRA ALPHA MIDDLE", strings.Join(strings.Fields(got[0]), " "),
		"source order and the output's own spelling, not alphabetical keys")
}

// Naming a column must not cost you the heading the output printed.
func TestTable_NamedColumnsKeepTheParseTitle(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "fixed"},
		Blocks: []viewspec.Block{{Kind: "table", Columns: []viewspec.Column{
			{Field: "names"}, {Field: "container id", Title: "id"}}}}}
	got := draw(t, spec, "CONTAINER ID   NAMES\na1b2c3         web\n", 40)
	require.Len(t, got, 2)
	assert.Equal(t, "NAMES id", strings.Join(strings.Fields(got[0]), " "),
		"the parse's title where the block gave none, the block's where it did")
}

// A long free-text column must not cost a short one its digits.
func TestTable_NarrowColumnsKeepTheirWidth(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "columns", Header: true},
		Blocks: []viewspec.Block{{Kind: "table"}}}
	got := draw(t, spec, "PID RSS COMMAND\n77612 450123 "+strings.Repeat("x", 200)+"\n", 60)
	require.Len(t, got, 2)
	assert.True(t, strings.HasPrefix(got[1], "77612 450123 x"), "got %q", got[1])
	assert.True(t, strings.HasSuffix(got[1], "…"), "the long column is the one cut")
}

func TestTree_DrawsFromADepthFieldOrFromPaths(t *testing.T) {
	byDepth := viewspec.Spec{Parse: viewspec.Parse{Kind: "indent"},
		Blocks: []viewspec.Block{{Kind: "tree", Field: "text", Depth: "depth"}}}
	assert.Equal(t, []string{
		"src",
		"├─ main.go",
		"└─ ui",
		"   └─ view.go",
	}, draw(t, byDepth, "src\n  main.go\n  ui\n    view.go\n", 40),
		"no bar under a branch that already closed")

	assert.Equal(t, []string{
		"src",
		"├─ ui",
		"│  └─ view.go",
		"└─ main.go",
	}, draw(t, byDepth, "src\n  ui\n    view.go\n  main.go\n", 40),
		"but a bar where the ancestor still has rows to come")

	byPath := viewspec.Spec{
		Parse:  viewspec.Parse{Kind: "lines", Pattern: `^(?P<path>\S.*)$`},
		Blocks: []viewspec.Block{{Kind: "tree", Field: "path"}}}
	assert.Equal(t, []string{
		"cmd",
		"└─ main.go",
		"ui",
		"└─ view.go",
	}, draw(t, byPath, "cmd\ncmd/main.go\nui\nui/view.go\n", 40),
		"depth from slashes, and only the leaf is drawn")
}

func TestTree_DepthIsCappedByTheFrame(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "columns", Fields: []string{"depth", "name"}},
		Blocks: []viewspec.Block{{Kind: "tree", Field: "name", Depth: "depth"}}}
	for _, width := range []int{1, 10, 30} {
		got := draw(t, spec, "0 root\n3000000000 deep\n4 four\n", width)
		require.Len(t, got, 3)
		for _, l := range got {
			assert.LessOrEqual(t, viewspec.Plain().Width(l), width, "%q", l)
		}
	}
}

func TestTree_StemsMatchTheShape(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "indent"},
		Blocks: []viewspec.Block{{Kind: "tree", Field: "text", Depth: "depth"}}}
	got := draw(t, spec, "root\n  a\n    a1\n    a2\n  b\n    b1\n", 40)
	assert.Equal(t, []string{
		"root",
		"├─ a",
		"│  ├─ a1",
		"│  └─ a2",
		"└─ b",
		"   └─ b1",
	}, got)
}

// The numbers in a meter are counted from rows, so model-written prose
// in the title cannot make the bar say something the output didn't.
func TestMeter_TitleCannotChangeTheCount(t *testing.T) {
	spec := viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{{
		Kind: "meter", Title: "all 99 packages passed", CountWhere: "status=ok", Of: "*"}}}
	got := draw(t, spec, goTest, 60)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "3/4")
	assert.NotContains(t, got[0], "99/")
}

func TestMeter_CountsHitsInsideItsDenominator(t *testing.T) {
	const states = "Name State\nx failed\ny running\nz running\n"
	got := draw(t, viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "meter", Title: "m", CountWhere: "state=failed", Of: "state=running"}}}, states, 40)
	require.Len(t, got, 1)
	assert.True(t, strings.HasSuffix(got[0], " 0/2"), "a hit the denominator excludes is not a hit: %q", got[0])
}

func TestMeter_OfMustNameAParsedField(t *testing.T) {
	c, err := viewspec.Compile(viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "meter", CountWhere: "state=ok", Of: "stat=running"}}})
	require.NoError(t, err)
	_, err = c.Bind("Name State\nx ok\n")
	var be *viewspec.BindError
	require.ErrorAs(t, err, &be)
	assert.Equal(t, "stat", be.Field)
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

func TestErrors_ColoursWholeLinesBySeverity(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "none"},
		Blocks: []viewspec.Block{{Kind: "errors"}}}
	r, err := bind(t, spec, "starting up\nWARNING: deprecated flag\npanic: nil map\n").
		Draw(viewspec.Frame{Width: 60, Paint: rolePainter{viewspec.Plain()}})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"default:starting up",
		"caution:WARNING: deprecated flag",
		"danger:panic: nil map",
	}, r.Lines, "whole lines, not matches: a traceback reads as a unit")
}

func TestJSON_IndentsWhatItCanAndPassesTheRestThrough(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "none"},
		Blocks: []viewspec.Block{{Kind: "json"}}}
	assert.Equal(t, []string{`{`, `  "b": 2`, `}`},
		draw(t, spec, `{"b":2}`, 40))
	assert.Equal(t, []string{"not json at all"},
		draw(t, spec, "not json at all\n", 40), "invalid JSON is still shown")
	assert.Equal(t, []string{`{`, `  "z": 12345678901234567890,`, `  "a": 1e-7`, `}`},
		draw(t, spec, `{"z":12345678901234567890,"a":1e-7}`, 40), "keys and numbers stay as printed")
}
