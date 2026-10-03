package viewspec_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

// A confidently wrong view is worse than none, so every unresolved
// binding fails the whole view rather than one block.
func TestBind_UnresolvedBindingDropsTheView(t *testing.T) {
	tests := []struct {
		name  string
		spec  viewspec.Spec
		field string
		at    string // "compile" or "bind"
	}{
		{
			name: "unknown block kind", at: "compile",
			spec: viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{{Kind: "hologram"}}},
		},
		{
			name: "pattern does not compile", at: "compile",
			spec: viewspec.Spec{Parse: viewspec.Parse{Kind: "lines", Pattern: `(?P<a>`},
				Blocks: []viewspec.Block{{Kind: "log"}}},
		},
		{
			name: "pattern has no named captures", at: "compile",
			spec: viewspec.Spec{Parse: viewspec.Parse{Kind: "lines", Pattern: `ok`},
				Blocks: []viewspec.Block{{Kind: "log"}}},
		},
		{
			name: "malformed where", at: "compile",
			spec: viewspec.Spec{Parse: linesParse(),
				Blocks: []viewspec.Block{{Kind: "list", Field: "pkg", Where: "status"}}},
		},
		{
			name: "column names a field the parse never produced", at: "bind", field: "elapsed",
			spec: viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{
				{Kind: "table", Columns: []viewspec.Column{{Field: "elapsed"}}}}},
		},
		{
			name: "list field missing", at: "bind", field: "owner",
			spec: viewspec.Spec{Parse: linesParse(),
				Blocks: []viewspec.Block{{Kind: "list", Field: "owner"}}},
		},
		{
			name: "accent field missing", at: "bind", field: "severity",
			spec: viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{{
				Kind: "list", Field: "pkg",
				Accent: &viewspec.Accent{Field: "severity",
					Map: map[string]viewspec.Role{"x": viewspec.RoleDanger}}}}},
		},
		{
			name: "where field missing", at: "bind", field: "tier",
			spec: viewspec.Spec{Parse: linesParse(),
				Blocks: []viewspec.Block{{Kind: "list", Field: "pkg", Where: "tier=1"}}},
		},
		{
			name: "count_where field missing", at: "bind", field: "outcome",
			spec: viewspec.Spec{Parse: linesParse(),
				Blocks: []viewspec.Block{{Kind: "meter", CountWhere: "outcome=ok"}}},
		},
		{
			name: "on_enter names a field the parse never produced", at: "bind", field: "id",
			spec: viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{
				{Kind: "list", Field: "pkg", OnEnter: "go test {id}"}}},
		},
		{
			name: "a row widget with no rows", at: "bind",
			spec: viewspec.Spec{Parse: linesParse(),
				Blocks: []viewspec.Block{{Kind: "list", Field: "pkg"}}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			output := goTest
			if strings.Contains(tc.name, "no rows") {
				output = "nothing matches this\n"
			}
			c, err := viewspec.Compile(tc.spec)
			if tc.at == "compile" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			_, err = c.Bind(output)
			require.Error(t, err)
			var be *viewspec.BindError
			require.ErrorAs(t, err, &be, "callers need to know which block failed")
			if tc.field != "" {
				assert.Equal(t, tc.field, be.Field)
			}
		})
	}
}

func TestCompile_NamesTheBlockThatFailed(t *testing.T) {
	_, err := viewspec.Compile(viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "list", Field: "a"}, {Kind: "list", Field: "b"}, {Kind: "nope"}}})
	var be *viewspec.BindError
	require.ErrorAs(t, err, &be)
	assert.Equal(t, 2, be.Block)
}

// A kind that cannot report a cursor cannot act on a row either, so
// on_enter there fails at compile time.
func TestOnEnter_RefusedWhereItCouldNotWork(t *testing.T) {
	for _, block := range []viewspec.Block{
		{Kind: "histogram", Field: "status", OnEnter: "x {pkg}"},
		{Kind: "meter", CountWhere: "status=ok", Of: "*", OnEnter: "x {pkg}"},
		{Kind: "badges", Field: "status", OnEnter: "x {pkg}"},
		{Kind: "stack", Columns: twoCols("pkg", "secs"), OnEnter: "x {pkg}"},
		{Kind: "scatter", Columns: twoCols("secs", "secs"), OnEnter: "x {pkg}"},
	} {
		_, err := viewspec.Compile(viewspec.Spec{Parse: linesParse(),
			Blocks: []viewspec.Block{block}})
		require.Error(t, err, block.Kind)
		assert.Contains(t, err.Error(), "one row per line", block.Kind)
	}
}

// A header is read during Bind, and once lived on the Compiled, so two
// outputs bound at once swapped each other's columns.
func TestBind_IsSafeOnOneCompiledAcrossGoroutines(t *testing.T) {
	aligned := [2]string{"ZEBRA  ALPHA\n1      2\n", "MIDDLE  OTHER\n3       4\n"}
	tests := []struct {
		parse   viewspec.Parse
		outputs [2]string
	}{
		{viewspec.Parse{Kind: "columns", Header: true}, aligned},
		{viewspec.Parse{Kind: "columns", Header: true, Skip: 1},
			[2]string{"banner\n" + aligned[0], "banner\n" + aligned[1]}},
		{viewspec.Parse{Kind: "delimited", Sep: ",", Header: true},
			[2]string{"ZEBRA,ALPHA\n1,2\n", "MIDDLE,OTHER\n3,4\n"}},
		{viewspec.Parse{Kind: "fixed"}, aligned},
		{viewspec.Parse{Kind: "box"}, [2]string{"| ZEBRA | ALPHA |\n| 1 | 2 |\n", "| MIDDLE | OTHER |\n| 3 | 4 |\n"}},
	}
	for _, tc := range tests {
		t.Run(tc.parse.Kind, func(t *testing.T) {
			c, err := viewspec.Compile(viewspec.Spec{Parse: tc.parse, Blocks: []viewspec.Block{{Kind: "table"}}})
			require.NoError(t, err)
			want := [2]string{"ZEBRA ALPHA", "MIDDLE OTHER"}
			var wg sync.WaitGroup
			for i := range 64 {
				wg.Go(func() {
					b, err := c.Bind(tc.outputs[i%2])
					if !assert.NoError(t, err) { //nolint:testifylint // runs off the test goroutine
						return
					}
					r, err := b.Draw(viewspec.Frame{Width: 40, Paint: viewspec.Plain()})
					if assert.NoError(t, err) && assert.NotEmpty(t, r.Lines) {
						assert.Equal(t, want[i%2], strings.Join(strings.Fields(r.Lines[0]), " "))
					}
				})
			}
			wg.Wait()
		})
	}
}

func TestSort_NumericComparesAsNumbers(t *testing.T) {
	spec := viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{{
		Kind: "list", Field: "secs",
		Sort: &viewspec.Sort{Field: "secs", Numeric: true}}}}
	assert.Equal(t, []string{"0.412", "1.203", "9.500", "10.200"},
		draw(t, spec, goTest, 20), "9.5 sorts before 10.2")

	spec.Blocks[0].Sort.Numeric = false
	assert.Equal(t, []string{"0.412", "1.203", "10.200", "9.500"},
		draw(t, spec, goTest, 20), "as strings, 10.2 sorts before 9.5")

	spec.Blocks[0].Sort = &viewspec.Sort{Field: "secs", Numeric: true, Desc: true}
	assert.Equal(t, []string{"10.200", "9.500", "1.203", "0.412"}, draw(t, spec, goTest, 20))
}

// With one cursor for the whole view, the block that can be acted on
// gets it, however the blocks are ordered.
func TestSelectable_PrefersTheActionableBlock(t *testing.T) {
	spec := viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{
		{Kind: "table", Columns: []viewspec.Column{{Field: "pkg"}}},
		{Kind: "list", Field: "pkg", Where: "status=FAIL", OnEnter: "go test -v {pkg}"},
	}}
	b := bind(t, spec, goTest)

	n, ok := b.SelectableRows()
	require.True(t, ok)
	assert.Equal(t, 1, n, "the list's one failing row, not the table's four")

	got, ok := b.Action(viewspec.Frame{Cursor: 0})
	require.True(t, ok)
	assert.Equal(t, "go test -v github.com/x/b", got)
}

func TestSelectableRows_ClampsWithoutKnowingTheShape(t *testing.T) {
	spec := viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{
		{Kind: "meter", CountWhere: "status=ok", Of: "*"},
		{Kind: "list", Field: "pkg", Where: "status=ok", OnEnter: "go test -v {pkg}"},
	}}
	n, ok := bind(t, spec, goTest).SelectableRows()
	require.True(t, ok)
	assert.Equal(t, 3, n, "counts the on_enter block's rows, not every parsed row")

	spec.Blocks[1].OnEnter = ""
	n, ok = bind(t, spec, goTest).SelectableRows()
	assert.True(t, ok, "a list is navigable with or without an action")
	assert.Equal(t, 3, n)

	spec.Blocks = spec.Blocks[:1]
	_, ok = bind(t, spec, goTest).SelectableRows()
	assert.False(t, ok, "a meter draws no cursor")
}

func TestAction_SubstitutesTheCursorRow(t *testing.T) {
	spec := viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{{
		Kind: "list", Field: "pkg", OnEnter: "go test -v {pkg}"}}}
	b := bind(t, spec, goTest)

	got, ok := b.Action(viewspec.Frame{Cursor: 1})
	require.True(t, ok)
	assert.Equal(t, "go test -v github.com/x/b", got)

	_, ok = b.Action(viewspec.Frame{Cursor: 99})
	assert.False(t, ok, "a cursor past the rows resolves to nothing")

	spec.Blocks[0].OnEnter = ""
	_, ok = bind(t, spec, goTest).Action(viewspec.Frame{Cursor: 0})
	assert.False(t, ok, "no on_enter means no action")
}

// A failing package's own output is what the human wants to read, and
// a view of the summary lines alone hid it.
func TestBound_HidesWhenMostLinesReachNoBlock(t *testing.T) {
	failing := goTest + strings.Repeat("    b_test.go:12: want 3, got 4\n", 6)
	summary := viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{{Kind: "table"}}}
	assert.False(t, bind(t, summary, goTest).Hides(), "every line is a row")
	assert.True(t, bind(t, summary, failing).Hides())

	withText := summary
	withText.Blocks = append(withText.Blocks, viewspec.Block{Kind: "log"})
	assert.False(t, bind(t, withText, failing).Hides(), "the text is drawn beneath")
	raw := viewspec.Spec{Parse: viewspec.Parse{Kind: "none"}, Blocks: []viewspec.Block{{Kind: "log"}}}
	assert.False(t, bind(t, raw, failing).Hides())

	// A JSON document spreads a record over lines, so counting them means nothing.
	doc := viewspec.Spec{Parse: viewspec.Parse{Kind: "json"}, Blocks: []viewspec.Block{{Kind: "table"}}}
	assert.False(t, bind(t, doc, "[\n  {\n    \"id\": 1\n  },\n  {\n    \"id\": 2\n  }\n]\n").Hides())
}

// Composing a view means asking which field to draw, and a field name
// on its own is thin. Sample is how a caller shows what one holds.
func TestSample_ShowsWhatAFieldHolds(t *testing.T) {
	b := bind(t, viewspec.Spec{Parse: linesParse(),
		Blocks: []viewspec.Block{{Kind: "table"}}}, goTest)

	got := b.Sample(2)
	require.Len(t, got, 2, "capped at what was asked for")
	assert.Equal(t, "github.com/x/a", got[0]["pkg"])
	assert.Equal(t, "FAIL", got[1]["status"])

	assert.Len(t, b.Sample(100), 4, "and never more than there are")

	// Nothing parsed means nothing to show, not a panic.
	none := bind(t, viewspec.Spec{Parse: viewspec.Parse{Kind: "none"},
		Blocks: []viewspec.Block{{Kind: "log"}}}, "some text\n")
	assert.Empty(t, none.Sample(3))
}

func TestSample_IsTheParseNotABlocksSlice(t *testing.T) {
	b := bind(t, viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{
		{Kind: "list", Field: "pkg", Where: "status=FAIL"}}}, goTest)
	got := b.Sample(4)
	require.Len(t, got, 4, "before the block's filter")
	got[0]["pkg"] = "edited"
	assert.Equal(t, "github.com/x/a", b.Sample(1)[0]["pkg"], "a caller's edit stays its own")
}

func TestSpec_CloneSharesNothing(t *testing.T) {
	orig := viewspec.Spec{Parse: viewspec.Parse{Kind: "columns", Fields: []string{"a"}},
		Blocks: []viewspec.Block{{Kind: "row", Panes: []viewspec.Pane{
			{Blocks: []viewspec.Block{{Kind: "table", Columns: cols("a"),
				Sort:   &viewspec.Sort{Field: "a"},
				Accent: &viewspec.Accent{Field: "a", Map: map[string]viewspec.Role{"x": viewspec.RoleSafe}}}}},
		}}}}
	c := orig.Clone()
	c.Parse.Fields[0] = "z"
	inner := &c.Blocks[0].Panes[0].Blocks[0]
	inner.Columns[0].Field = "z"
	inner.Sort.Field = "z"
	inner.Accent.Map["x"] = viewspec.RoleDanger
	c.Blocks[0].Panes[0].Weight = 9

	assert.Equal(t, "a", orig.Parse.Fields[0])
	was := orig.Blocks[0].Panes[0]
	assert.Equal(t, 0, was.Weight)
	assert.Equal(t, "a", was.Blocks[0].Columns[0].Field)
	assert.Equal(t, "a", was.Blocks[0].Sort.Field)
	assert.Equal(t, viewspec.RoleSafe, was.Blocks[0].Accent.Map["x"])
}
