package viewspec_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

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

func TestSample_IsTheParseNotABlocksSlice(t *testing.T) {
	b := bind(t, viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{
		{Kind: "list", Field: "pkg", Where: "status=FAIL"}}}, goTest)
	got := b.Sample(4)
	require.Len(t, got, 4, "before the block's filter")
	got[0]["pkg"] = "edited"
	assert.Equal(t, "github.com/x/a", b.Sample(1)[0]["pkg"], "a caller's edit stays its own")
}

func TestHeader_RepeatedTitlesKeepEveryColumn(t *testing.T) {
	tests := []struct {
		parse  viewspec.Parse
		output string
	}{
		{viewspec.Parse{Kind: "columns", Header: true}, "NAME name Name\na b c\n"},
		{viewspec.Parse{Kind: "delimited", Sep: ",", Header: true}, "NAME,name,Name\na,b,c\n"},
		{viewspec.Parse{Kind: "box"}, "| NAME | name | Name |\n| a | b | c |\n"},
		{viewspec.Parse{Kind: "json"}, `{"NAME":"a","name":"b","Name":"c"}`},
	}
	for _, tc := range tests {
		t.Run(tc.parse.Kind, func(t *testing.T) {
			b := bind(t, viewspec.Spec{Parse: tc.parse, Blocks: []viewspec.Block{{Kind: "table"}}}, tc.output)
			assert.Equal(t, []string{"name", "name_2", "name_3"}, b.Fields())
			for range 5 {
				assert.Equal(t, b.Sample(1), bind(t, viewspec.Spec{Parse: tc.parse,
					Blocks: []viewspec.Block{{Kind: "table"}}}, tc.output).Sample(1), "the same every run")
			}
		})
	}
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

func TestCompile_NamesTheBlockThatFailed(t *testing.T) {
	_, err := viewspec.Compile(viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "list", Field: "a"}, {Kind: "list", Field: "b"}, {Kind: "nope"}}})
	var be *viewspec.BindError
	require.ErrorAs(t, err, &be)
	assert.Equal(t, 2, be.Block)
}

// One cursor addresses one block, so only that block may light a row.
func TestDraw_OnlyTheSelectedBlockHighlights(t *testing.T) {
	spec := viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{
		{Kind: "list", Field: "name"}, {Kind: "list", Field: "name", OnEnter: "x {name}"}}}
	r, err := bind(t, spec, "Name\na\nb\n").Draw(viewspec.Frame{
		Width: 40, Focused: true, Cursor: 0, Paint: rolePainter{viewspec.Plain()}})
	require.NoError(t, err)
	var lit []int
	for i, l := range r.Lines {
		if strings.HasPrefix(l, "accent:") {
			lit = append(lit, i)
		}
	}
	assert.Equal(t, []int{2}, lit)
	assert.Equal(t, 2, r.CursorLine)
}
