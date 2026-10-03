package viewspec_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

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
