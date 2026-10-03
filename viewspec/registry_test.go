package viewspec_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

func TestRegistry_IsTheExtensionPoint(t *testing.T) {
	reg := viewspec.Standard()
	require.NoError(t, reg.Widget("table", viewspec.WidgetFunc(
		func(viewspec.Block, viewspec.Data, viewspec.Frame) ([]string, error) {
			return []string{"a better table"}, nil
		})))
	require.NoError(t, reg.Widget("hologram", viewspec.WidgetFunc(
		func(viewspec.Block, viewspec.Data, viewspec.Frame) ([]string, error) {
			return []string{"▁▂▃"}, nil
		})))

	spec := viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{
		{Kind: "table", Columns: []viewspec.Column{{Field: "pkg"}}}, {Kind: "hologram"}}}
	c, err := viewspec.Compile(spec, viewspec.WithRegistry(reg))
	require.NoError(t, err)
	b, err := c.Bind(goTest)
	require.NoError(t, err)
	r, err := b.Draw(viewspec.Frame{Width: 40, Paint: viewspec.Plain()})
	require.NoError(t, err)
	assert.Equal(t, []string{"a better table", "▁▂▃"}, r.Lines,
		"a registered widget overrides a built-in of the same kind")

	_, err = viewspec.Compile(spec)
	assert.Error(t, err, "the standard registry is unaffected by another's registrations")
}

// A registered widget is part of the vocabulary at once, beside the built-ins.
func TestRegistry_ListsWhatIsRegistered(t *testing.T) {
	reg := viewspec.Standard()
	require.NoError(t, reg.Widget("hologram", viewspec.WidgetFunc(
		func(viewspec.Block, viewspec.Data, viewspec.Frame) ([]string, error) { return nil, nil })))
	assert.Contains(t, reg.Kinds(), "hologram")
	assert.Contains(t, reg.Kinds(), "table")
	assert.Contains(t, reg.ParseKinds(), "columns")
}

func TestRegistry_SelectsIsTheKindsThatDrawARowPerLine(t *testing.T) {
	reg := viewspec.Standard()
	assert.True(t, reg.Selects("list"))
	assert.True(t, reg.Selects("table"))
	assert.False(t, reg.Selects("histogram"))
	assert.False(t, reg.Selects("nonsense"))
}

// Every built-in says what it is for and what it is not, or the judge is
// never offered it.
func TestRegistry_EveryBuiltinDescribesItself(t *testing.T) {
	reg := viewspec.Standard()
	for _, kind := range reg.Kinds() {
		d, ok := reg.Describe(kind)
		require.True(t, ok, "%s describes itself", kind)
		assert.NotEmpty(t, d.What, kind)
		assert.NotEmpty(t, d.NotFor, "%s names what it is confused with", kind)
		assert.NotEmpty(t, d.Examples, kind)
	}

	// A widget that says nothing is absent rather than blank.
	require.NoError(t, reg.Widget("hologram", viewspec.WidgetFunc(
		func(viewspec.Block, viewspec.Data, viewspec.Frame) ([]string, error) { return nil, nil })))
	_, ok := reg.Describe("hologram")
	assert.False(t, ok)
}

// A kind that cannot be drawn without a field says so, and the slot count
// decides the block's shape: one fills Field, more fill Columns.
func TestSchema_KindsStateWhatTheyCannotBeDrawnWithout(t *testing.T) {
	reg := viewspec.Standard()
	for kind, want := range map[string][]string{
		"list":      {"field"},
		"tree":      {"field"},
		"badges":    {"field"},
		"sparkline": {"field"},
		"bar":       {"label", "value"},
		"keyvalue":  {"label", "value"},
		"scatter":   {"x", "y"},
		"gantt":     {"label", "start", "length"},
		"delta":     {"label", "from", "to"},
	} {
		d, ok := reg.Describe(kind)
		require.True(t, ok, kind)
		var names []string
		for _, s := range d.Needs {
			assert.NotEmpty(t, s.What, "%s slot %s says what it holds", kind, s.Name)
			names = append(names, s.Name)
		}
		assert.Equal(t, want, names, kind)
	}

	// Widgets that draw raw bytes name no field.
	for _, kind := range []string{"log", "errors", "json", "diff", "code", "table"} {
		d, _ := reg.Describe(kind)
		assert.Empty(t, d.Needs, "%s draws without a named field", kind)
	}

	// Nor do the ones needing something a field choice cannot supply.
	for _, kind := range []string{"meter", "stat", "text", "dots", viewspec.RowKind} {
		d, _ := reg.Describe(kind)
		assert.Empty(t, d.Needs, "%s needs more than a field, so it declares no slots", kind)
	}
}

func TestSubset_NarrowsWidgetsAndValidatesAgainstTheSame(t *testing.T) {
	reg := viewspec.Standard().Subset("list", "meter", "nope")
	assert.Equal(t, []string{"list", "meter"}, reg.Kinds())
	assert.Equal(t, viewspec.Standard().ParseKinds(), reg.ParseKinds(),
		"parse kinds are not what gets narrowed")

	spec := viewspec.Spec{Parse: linesParse(),
		Blocks: []viewspec.Block{{Kind: "table", Columns: []viewspec.Column{{Field: "pkg"}}}}}
	_, err := viewspec.Compile(spec, viewspec.WithRegistry(reg))
	require.Error(t, err, "a kind outside the subset will not compile")

	assert.Len(t, reg.Kinds(), 2, "and only what it may use is described")
}

func TestContainer_IsAnExtensionPoint(t *testing.T) {
	reg := viewspec.Standard()
	require.NoError(t, reg.Widget("stacked", stacked{}))

	spec := viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{{
		Kind: "stacked",
		Panes: []viewspec.Pane{
			{Blocks: []viewspec.Block{{Kind: "badges", Field: "status"}}},
			{Blocks: []viewspec.Block{{Kind: "list", Field: "pkg", OnEnter: "x {pkg}"}}},
		},
	}}}
	c, err := viewspec.Compile(spec, viewspec.WithRegistry(reg))
	require.NoError(t, err)
	b, err := c.Bind(goTest)
	require.NoError(t, err)

	r, err := b.Draw(viewspec.Frame{Width: 60, Cursor: 1, Paint: viewspec.Plain()})
	require.NoError(t, err)
	assert.Equal(t, "== top ==", r.Lines[0], "the consumer's own arrangement")
	assert.Contains(t, r.Lines, "--")
	assert.Equal(t, 4, r.CursorLine, "the container said where its second pane starts")

	// It is a container to every rule, not just to Draw.
	_, err = viewspec.Compile(viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{{Kind: viewspec.RowKind,
		Panes: []viewspec.Pane{{Blocks: []viewspec.Block{{Kind: "list", Field: "pkg"}}}, {Blocks: spec.Blocks}}}}},
		viewspec.WithRegistry(reg))
	require.ErrorContains(t, err, "do not nest", "a container cannot sit inside a pane")
	_, err = viewspec.Compile(viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{
		{Kind: "stacked", Panes: []viewspec.Pane{{Blocks: []viewspec.Block{{Kind: "log"}}}}}}},
		viewspec.WithRegistry(reg))
	assert.ErrorContains(t, err, "stacked needs two panes", "its own Accept decides the shape")
}

// stacked is a layout registered from outside, proving the interpreter
// asks the widget what it is rather than knowing "row" and "panel" by name.
type stacked struct{}

func (stacked) Draw(viewspec.Block, viewspec.Data, viewspec.Frame) ([]string, error) {
	return nil, errors.New("arranged, not drawn")
}

func (stacked) Accept(panes []viewspec.Pane) error {
	if len(panes) < 2 {
		return errors.New("stacked needs two panes")
	}
	return nil
}

func (stacked) Widths(panes []viewspec.Pane, total int) ([]int, error) {
	out := make([]int, len(panes))
	for i := range out {
		out[i] = total
	}
	return out, nil
}

func (stacked) Arrange(cols [][]string, _ []int, _ viewspec.Block, _ viewspec.Frame) ([]string, []int) {
	lines := []string{"== top =="}
	at := make([]int, len(cols))
	for i, col := range cols {
		if i > 0 {
			lines = append(lines, "--")
		}
		at[i] = len(lines)
		lines = append(lines, col...)
	}
	return lines, at
}
