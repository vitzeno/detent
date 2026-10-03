package viewspec_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

func TestDraw_Widgets(t *testing.T) {
	tests := []struct {
		name  string
		block viewspec.Block
		want  []string
	}{
		{
			name:  "text renders its title",
			block: viewspec.Block{Kind: "text", Title: "4 packages"},
			want:  []string{"4 packages"},
		},
		{
			name: "table heads columns and pads cells",
			block: viewspec.Block{Kind: "table", Columns: []viewspec.Column{
				{Field: "status"}, {Field: "pkg", Title: "package"}}},
			want: []string{
				"status package       ",
				"ok     github.com/x/a",
				"FAIL   github.com/x/b",
				"ok     github.com/x/c",
				"ok     github.com/x/d",
			},
		},
		{
			name:  "list draws one field per row",
			block: viewspec.Block{Kind: "list", Field: "pkg", Where: "status=FAIL"},
			want:  []string{"github.com/x/b"},
		},
		{
			name: "keyvalue aligns on the widest label",
			block: viewspec.Block{Kind: "keyvalue", Columns: []viewspec.Column{
				{Field: "status"}, {Field: "pkg"}}},
			want: []string{
				"ok    github.com/x/a", "FAIL  github.com/x/b",
				"ok    github.com/x/c", "ok    github.com/x/d",
			},
		},
		{
			name:  "meter counts rows",
			block: viewspec.Block{Kind: "meter", Title: "passed", CountWhere: "status=ok", Of: "*"},
			want:  []string{"passed █████████████████████░░░░░░░░ 3/4"},
		},
		{
			name:  "badges counts distinct values",
			block: viewspec.Block{Kind: "badges", Field: "status"},
			want:  []string{"FAIL 1  ok 3"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := viewspec.Spec{Version: 1, Parse: linesParse(),
				Blocks: []viewspec.Block{tc.block}}
			assert.Equal(t, tc.want, draw(t, spec, goTest, 40))
		})
	}
}

func TestDraw_RawWidgets(t *testing.T) {
	const diff = "diff --git a/x b/x\n@@ -1 +1 @@\n-old\n+new\n"
	spec := viewspec.Spec{Version: 1, Parse: viewspec.Parse{Kind: "none"},
		Blocks: []viewspec.Block{{Kind: "code"}}}
	assert.Equal(t, []string{"   1 -old", "   2 +new"},
		draw(t, spec, "-old\n+new\n", 40), "code numbers its lines")

	spec.Blocks = []viewspec.Block{{Kind: "diff"}}
	assert.Len(t, draw(t, spec, diff, 40), 4, "diff keeps every line")

	spec.Blocks = []viewspec.Block{{Kind: "log"}}
	assert.Equal(t, []string{"a", "b"}, draw(t, spec, "a\nb\n", 40))
}

// Each of these panicked on output any shell prints: a negative number,
// a filter matching nothing, a count outside its denominator.
func TestDraw_OrdinaryOutputNeverPanics(t *testing.T) {
	const signed = "Name Was Now\na -5 3\nb 10 -2\nc 0 0\n"
	const states = "Name State Start Len\nx failed 0 5\ny running 2 -9\nz running 10 1\n"
	tests := []struct {
		name   string
		output string
		block  viewspec.Block
	}{
		{"bar with a negative value", signed, viewspec.Block{Kind: "bar", Columns: cols("name", "was")}},
		{"bar with only negatives", "Name V\na -5\nb -1\n", viewspec.Block{Kind: "bar", Columns: cols("name", "v")}},
		{"diverge with negatives", signed, viewspec.Block{Kind: "diverge", Columns: cols("name", "was", "now")}},
		{"meter counting outside its denominator", states,
			viewspec.Block{Kind: "meter", CountWhere: "state=failed", Of: "state=running"}},
		{"gantt with a negative length", states, viewspec.Block{Kind: "gantt", Columns: cols("name", "start", "len")}},
		{"heatmap filtered to nothing", states,
			viewspec.Block{Kind: "heatmap", Columns: cols("name", "state"), Where: "state=gone"}},
		{"series filtered to nothing", states,
			viewspec.Block{Kind: "series", Columns: cols("state", "len"), Where: "state=gone"}},
		{"sparkline filtered to nothing", states,
			viewspec.Block{Kind: "sparkline", Field: "len", Where: "state=gone"}},
		{"scatter filtered to nothing", states,
			viewspec.Block{Kind: "scatter", Columns: cols("start", "len"), Where: "state=gone"}},
		{"stack filtered to nothing", states,
			viewspec.Block{Kind: "stack", Columns: cols("name", "len"), Where: "state=gone"}},
		{"timeline filtered to nothing", states,
			viewspec.Block{Kind: "timeline", Columns: cols("name", "start"), Where: "state=gone"}},
		{"boxplot with negatives", signed, viewspec.Block{Kind: "boxplot", Columns: cols("name", "now")}},
		{"number too large to be finite", "Name V\na 1" + strings.Repeat("0", 300) + "E\nb 2\n",
			viewspec.Block{Kind: "sparkline", Field: "v"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := viewspec.Spec{Parse: colsParse(), Blocks: []viewspec.Block{tc.block}}
			for _, width := range []int{1, 8, 40, 120} {
				b := bind(t, spec, tc.output)
				var r viewspec.Render
				require.NotPanics(t, func() {
					r, _ = b.Draw(viewspec.Frame{Width: width, Paint: viewspec.Plain()})
				}, "width %d", width)
				for _, l := range r.Lines {
					assert.LessOrEqual(t, viewspec.Plain().Width(l), width, "%q", l)
				}
			}
		})
	}
}

// Height 0 draws the view whole, which is what a caller scrolling it
// through its own viewport needs.
func TestDraw_AlwaysDrawsWholeSoTheCallerCanWindow(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "none"},
		Blocks: []viewspec.Block{{Kind: "log"}}}
	long := strings.Repeat("a line\n", 50)

	r, err := bind(t, spec, long).Draw(viewspec.Frame{Width: 40, Paint: viewspec.Plain()})
	require.NoError(t, err)
	assert.Len(t, r.Lines, 50)

	// Height says how tall the pane is, for the widgets that grow into
	// it. Clipping to it would stop a long view scrolling.
	r, err = bind(t, spec, long).Draw(viewspec.Frame{Width: 40, Height: 10, Paint: viewspec.Plain()})
	require.NoError(t, err)
	assert.Len(t, r.Lines, 50, "a height never bounds it")
}

// A caller windowing a tall view has to scroll to the selection
// without knowing how the blocks above it were laid out.
func TestRender_ReportsWhereTheCursorLanded(t *testing.T) {
	spec := viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{
		{Kind: "meter", CountWhere: "status=ok", Of: "*"},
		{Kind: "table", Columns: []viewspec.Column{{Field: "pkg"}},
			OnEnter: "go test -v {pkg}"},
	}}
	b := bind(t, spec, goTest)

	r, err := b.Draw(viewspec.Frame{Width: 40, Cursor: 2, Paint: viewspec.Plain()})
	require.NoError(t, err)
	assert.Equal(t, 4, r.CursorLine, "one meter line, one header, then row 2")
	assert.Contains(t, r.Lines[r.CursorLine], "github.com/x/c")

	r, err = bind(t, spec, goTest).Draw(viewspec.Frame{Width: 40, Cursor: 99, Paint: viewspec.Plain()})
	require.NoError(t, err)
	assert.Equal(t, -1, r.CursorLine, "a cursor past the rows lands nowhere")

	spec.Blocks = spec.Blocks[:1]
	r, err = bind(t, spec, goTest).Draw(viewspec.Frame{Width: 40, Paint: viewspec.Plain()})
	require.NoError(t, err)
	assert.Equal(t, -1, r.CursorLine, "a meter alone draws no cursor")
}

// Any widget drawing one row per record is navigable, so on_enter
// works wherever a spec puts it.
func TestCursor_EveryRowWidgetReportsIt(t *testing.T) {
	blocks := map[string]viewspec.Block{
		"list":     {Kind: "list", Field: "pkg", OnEnter: "x {pkg}"},
		"table":    {Kind: "table", Columns: []viewspec.Column{{Field: "pkg"}}, OnEnter: "x {pkg}"},
		"bar":      {Kind: "bar", Columns: []viewspec.Column{{Field: "pkg"}, {Field: "secs"}}, OnEnter: "x {pkg}"},
		"keyvalue": {Kind: "keyvalue", Columns: []viewspec.Column{{Field: "pkg"}, {Field: "secs"}}, OnEnter: "x {pkg}"},
		"tree":     {Kind: "tree", Field: "pkg", OnEnter: "x {pkg}"},
		"gauge":    {Kind: "gauge", Columns: twoCols("pkg", "secs"), OnEnter: "x {pkg}"},
		"timeline": {Kind: "timeline", Columns: twoCols("pkg", "secs"), OnEnter: "x {pkg}"},
		"flow":     {Kind: "flow", Field: "pkg", OnEnter: "x {pkg}"},
		"gantt": {Kind: "gantt", Columns: threeCols("pkg", "secs", "secs"),
			OnEnter: "x {pkg}"},
		"diverge": {Kind: "diverge", Columns: threeCols("pkg", "secs", "secs"),
			OnEnter: "x {pkg}"},
		"delta": {Kind: "delta", Columns: threeCols("pkg", "secs", "secs"),
			OnEnter: "x {pkg}"},
		"dots": {Kind: "dots", Field: "pkg", OnEnter: "x {pkg}",
			Accent: &viewspec.Accent{Field: "status",
				Map: map[string]viewspec.Role{"ok": viewspec.RoleSafe, "FAIL": viewspec.RoleDanger}}},
	}
	for kind, block := range blocks {
		t.Run(kind, func(t *testing.T) {
			b := bind(t, viewspec.Spec{Parse: linesParse(),
				Blocks: []viewspec.Block{block}}, goTest)
			r, err := b.Draw(viewspec.Frame{Width: 60, Cursor: 1, Paint: viewspec.Plain()})
			require.NoError(t, err)
			assert.GreaterOrEqual(t, r.CursorLine, 0, "%s draws a cursor", kind)

			got, ok := b.Action(viewspec.Frame{Cursor: 1})
			require.True(t, ok)
			assert.Equal(t, "x github.com/x/b", got)
		})
	}

	// Summaries have no rows to address.
	for _, block := range []viewspec.Block{
		{Kind: "meter", CountWhere: "status=ok", Of: "*"},
		{Kind: "badges", Field: "status"},
		{Kind: "sparkline", Field: "secs"},
		{Kind: "histogram", Field: "status"},
		{Kind: "boxplot", Columns: twoCols("status", "secs")},
		{Kind: "series", Columns: twoCols("status", "secs")},
		{Kind: "scatter", Columns: twoCols("secs", "secs")},
		{Kind: "heatmap", Columns: twoCols("status", "pkg")},
		{Kind: "stack", Columns: twoCols("pkg", "secs")},
		{Kind: "log"},
	} {
		b := bind(t, viewspec.Spec{Parse: linesParse(),
			Blocks: []viewspec.Block{block}}, goTest)
		_, ok := b.SelectableRows()
		assert.False(t, ok, "%s summarises rather than addressing rows", block.Kind)
	}
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

func TestRole_IsNamedOnTheWire(t *testing.T) {
	b, err := json.Marshal(viewspec.RoleDanger)
	require.NoError(t, err)
	assert.JSONEq(t, `"danger"`, string(b))

	var r viewspec.Role
	require.NoError(t, json.Unmarshal([]byte(`"caution"`), &r))
	assert.Equal(t, viewspec.RoleCaution, r)

	require.Error(t, json.Unmarshal([]byte(`"#ff0000"`), &r), "a colour is not a role")
	require.Error(t, json.Unmarshal([]byte(`7`), &r), "a number is not a role")
}
