package viewspec_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

func TestRow_LaysPanesSideBySide(t *testing.T) {
	got := draw(t, rowSpec(), goTest, 60)
	require.Len(t, got, 4, "as tall as its tallest pane, not the sum")

	assert.Contains(t, got[0], "FAIL 1", "the left pane's first line")
	assert.Contains(t, got[0], "github.com/x/a", "and the right pane's, on the same line")
	assert.Contains(t, got[1], "ok ", "left pane continues")
	assert.Contains(t, got[1], "github.com/x/b")
	assert.NotContains(t, got[2], "FAIL 1", "the left pane ran out; the right did not")
	assert.Contains(t, got[2], "github.com/x/c")

	for _, l := range got {
		assert.LessOrEqual(t, len([]rune(l)), 60, "never wider than the frame")
	}
}

// Weight shares the width, so a table can have room beside a summary.
func TestRow_WeightSharesTheWidth(t *testing.T) {
	spec := viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{{
		Kind: viewspec.RowKind,
		Panes: []viewspec.Pane{
			{Weight: 1, Blocks: []viewspec.Block{{Kind: "list", Field: "pkg"}}},
			{Weight: 3, Blocks: []viewspec.Block{{Kind: "list", Field: "pkg"}}},
		},
	}}}
	got := draw(t, spec, goTest, 61)
	require.NotEmpty(t, got)
	// 60 usable after the gutter: 15 and 45.
	assert.Equal(t, "github.com/x/a", strings.TrimSpace(string([]rune(got[0])[:15])))
	assert.Equal(t, "github.com/x/a", strings.TrimSpace(string([]rune(got[0])[16:])))
}

// A pane's cursor is the row's cursor: joining horizontally keeps line
// indexes, so a caller can still scroll to the selection.
func TestRow_CursorSurvivesTheLayout(t *testing.T) {
	b := bind(t, rowSpec(), goTest)

	n, ok := b.SelectableRows()
	require.True(t, ok, "selection reaches into panes")
	assert.Equal(t, 4, n)

	r, err := b.Draw(viewspec.Frame{Width: 60, Cursor: 2, Paint: viewspec.Plain()})
	require.NoError(t, err)
	assert.Equal(t, 2, r.CursorLine)
	assert.Contains(t, r.Lines[r.CursorLine], "github.com/x/c")

	got, ok := b.Action(viewspec.Frame{Cursor: 2})
	require.True(t, ok)
	assert.Equal(t, "go test -v github.com/x/c", got)
}

func TestRow_RefusesShapesItCannotDraw(t *testing.T) {
	leaf := []viewspec.Block{{Kind: "list", Field: "pkg"}}
	tests := []struct {
		name  string
		block viewspec.Block
	}{
		{"a row with one pane", viewspec.Block{Kind: viewspec.RowKind,
			Panes: []viewspec.Pane{{Blocks: leaf}}}},
		{"a row with an empty pane", viewspec.Block{Kind: viewspec.RowKind,
			Panes: []viewspec.Pane{{Blocks: leaf}, {}}}},
		{"a row inside a row", viewspec.Block{Kind: viewspec.RowKind,
			Panes: []viewspec.Pane{{Blocks: leaf}, {Blocks: []viewspec.Block{
				{Kind: viewspec.RowKind, Panes: []viewspec.Pane{{Blocks: leaf}, {Blocks: leaf}}}}}}}},
		{"panes on something that is not a row", viewspec.Block{Kind: "list", Field: "pkg",
			Panes: []viewspec.Pane{{Blocks: leaf}, {Blocks: leaf}}}},
		{"a negative weight", viewspec.Block{Kind: viewspec.RowKind,
			Panes: []viewspec.Pane{{Weight: -1, Blocks: leaf}, {Blocks: leaf}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := viewspec.Compile(viewspec.Spec{Parse: linesParse(),
				Blocks: []viewspec.Block{tc.block}})
			assert.Error(t, err)
		})
	}
}

// Nesting stops at one: a pane holds leaves, never another container.
func TestCompile_ContainersDoNotNest(t *testing.T) {
	leaf := viewspec.Block{Kind: "list", Field: "line"}
	pane := func(b viewspec.Block) viewspec.Pane { return viewspec.Pane{Blocks: []viewspec.Block{b}} }
	ok := viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{{Kind: viewspec.RowKind,
		Panes: []viewspec.Pane{pane(leaf), pane(leaf)}}}}
	_, err := viewspec.Compile(ok)
	require.NoError(t, err, "a top-level row of leaves")

	nested := viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{{Kind: viewspec.RowKind,
		Panes: []viewspec.Pane{pane(leaf), pane(viewspec.Block{Kind: viewspec.RowKind,
			Panes: []viewspec.Pane{pane(leaf), pane(leaf)}})}}}}
	_, err = viewspec.Compile(nested)
	assert.ErrorContains(t, err, "do not nest")
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

func rowSpec() viewspec.Spec {
	return viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{{
		Kind: viewspec.RowKind,
		Panes: []viewspec.Pane{
			{Weight: 1, Blocks: []viewspec.Block{
				{Kind: "badges", Field: "status"},
				{Kind: "meter", Title: "ok", CountWhere: "status=ok", Of: "*"},
			}},
			{Weight: 2, Blocks: []viewspec.Block{
				{Kind: "list", Field: "pkg", OnEnter: "go test -v {pkg}"},
			}},
		},
	}}}
}
