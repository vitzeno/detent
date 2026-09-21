package viewspec_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

const goTest = `ok  	github.com/x/a	0.412s
FAIL	github.com/x/b	1.203s
ok  	github.com/x/c	9.500s
ok  	github.com/x/d	10.200s
`

func linesParse() viewspec.Parse {
	return viewspec.Parse{Kind: "lines",
		Pattern: `^(?P<status>ok|FAIL)\s+(?P<pkg>\S+)\s+(?P<secs>[\d.]+)s`}
}

// bind is the whole data half in one call, and it needs no Painter at
// all — which is the reason Painter lives on Frame.
func bind(t *testing.T, spec viewspec.Spec, output string) *viewspec.Bound {
	t.Helper()
	c, err := viewspec.Compile(spec)
	require.NoError(t, err)
	b, err := c.Bind(output)
	require.NoError(t, err)
	return b
}

func draw(t *testing.T, spec viewspec.Spec, output string, width int) []string {
	t.Helper()
	r, err := bind(t, spec, output).Draw(viewspec.Frame{Width: width, Paint: viewspec.Plain()})
	require.NoError(t, err)
	return r.Lines
}

func TestExtract_Kinds(t *testing.T) {
	tests := []struct {
		name   string
		parse  viewspec.Parse
		output string
		want   []viewspec.Row
	}{
		{
			name: "lines names each capture", parse: linesParse(), output: goTest,
			want: []viewspec.Row{
				{"status": "ok", "pkg": "github.com/x/a", "secs": "0.412"},
				{"status": "FAIL", "pkg": "github.com/x/b", "secs": "1.203"},
				{"status": "ok", "pkg": "github.com/x/c", "secs": "9.500"},
				{"status": "ok", "pkg": "github.com/x/d", "secs": "10.200"},
			},
		},
		{
			name:   "columns lowercases the header and joins the tail",
			parse:  viewspec.Parse{Kind: "columns", Header: true},
			output: "NAME   STATUS\napi    Up 3 hours\ndb     Exited (0)\n",
			want: []viewspec.Row{
				{"name": "api", "status": "Up 3 hours"},
				{"name": "db", "status": "Exited (0)"},
			},
		},
		{
			name:   "json reads an array of objects",
			parse:  viewspec.Parse{Kind: "json"},
			output: `[{"Name":"api","Port":8080},{"Name":"db","Port":5432}]`,
			want: []viewspec.Row{
				{"name": "api", "port": "8080"},
				{"name": "db", "port": "5432"},
			},
		},
		{
			name:   "skip drops a banner before parsing",
			parse:  viewspec.Parse{Kind: "columns", Header: true, Skip: 1},
			output: "some banner\nNAME  STATUS\napi   Up\n",
			want:   []viewspec.Row{{"name": "api", "status": "Up"}},
		},
		{
			name: "none produces nothing", parse: viewspec.Parse{Kind: "none"},
			output: goTest, want: []viewspec.Row{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := viewspec.Spec{Version: 1, Parse: tc.parse,
				Blocks: []viewspec.Block{{Kind: "log"}}}
			b := bind(t, spec, tc.output)
			got, err := rowsOf(spec, tc.output)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			if len(tc.want) > 0 {
				assert.NotEmpty(t, b.Fields())
			}
		})
	}
}

// rowsOf reaches the parsed rows through the only surface that exposes
// them, so the test checks what a widget would actually receive.
func rowsOf(spec viewspec.Spec, output string) ([]viewspec.Row, error) {
	var got []viewspec.Row
	reg := viewspec.Standard()
	err := reg.Widget("log", viewspec.WidgetFunc(
		func(_ viewspec.Block, d viewspec.Data, _ viewspec.Frame) ([]string, error) {
			got = d.Rows
			return nil, nil
		}))
	if err != nil {
		return nil, err
	}
	c, err := viewspec.Compile(spec, viewspec.WithRegistry(reg))
	if err != nil {
		return nil, err
	}
	b, err := c.Bind(output)
	if err != nil {
		return nil, err
	}
	if _, err := b.Draw(viewspec.Frame{Width: 40, Paint: viewspec.Plain()}); err != nil {
		return nil, err
	}
	return got, nil
}

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
			want:  []string{"passed ███████████████░░░░░ 3/4"},
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

// Register a widget and the schema handed to a model includes it,
// which is the only thing stopping the generator drifting from the
// interpreter.
func TestSchema_DescribesTheRegisteredVocabulary(t *testing.T) {
	reg := viewspec.Standard()
	require.NoError(t, reg.Widget("hologram", viewspec.WidgetFunc(
		func(viewspec.Block, viewspec.Data, viewspec.Frame) ([]string, error) { return nil, nil })))

	raw, err := json.Marshal(reg.Schema())
	require.NoError(t, err)
	var s map[string]any
	require.NoError(t, json.Unmarshal(raw, &s))

	blocks := s["properties"].(map[string]any)["blocks"].(map[string]any)
	kinds := blocks["items"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)["enum"]
	assert.ElementsMatch(t, toStrings(reg.Kinds()), kinds)
	assert.Contains(t, toStrings(reg.Kinds()), "hologram")

	parse := s["properties"].(map[string]any)["parse"].(map[string]any)
	assert.ElementsMatch(t, toStrings(reg.ParseKinds()),
		parse["properties"].(map[string]any)["kind"].(map[string]any)["enum"])
}

func toStrings(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func TestRole_IsNamedOnTheWire(t *testing.T) {
	b, err := json.Marshal(viewspec.RoleDanger)
	require.NoError(t, err)
	assert.JSONEq(t, `"danger"`, string(b))

	var r viewspec.Role
	require.NoError(t, json.Unmarshal([]byte(`"caution"`), &r))
	assert.Equal(t, viewspec.RoleCaution, r)

	assert.Error(t, json.Unmarshal([]byte(`"#ff0000"`), &r), "a colour is not a role")
	assert.Error(t, json.Unmarshal([]byte(`7`), &r), "a number is not a role")
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

// Height 0 draws the view whole, which is what a caller scrolling it
// through its own viewport needs.
func TestDraw_HeightZeroDrawsEverything(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "none"},
		Blocks: []viewspec.Block{{Kind: "log"}}}
	long := strings.Repeat("a line\n", 50)

	r, err := bind(t, spec, long).Draw(viewspec.Frame{Width: 40, Paint: viewspec.Plain()})
	require.NoError(t, err)
	assert.Len(t, r.Lines, 50)

	r, err = bind(t, spec, long).Draw(viewspec.Frame{Width: 40, Height: 10, Paint: viewspec.Plain()})
	require.NoError(t, err)
	assert.Len(t, r.Lines, 10, "a height still bounds it")
}

// rolePainter makes roles visible to assertions, which Plain cannot —
// it paints nothing, on purpose, so golden files stay readable.
type rolePainter struct{ viewspec.Painter }

func (p rolePainter) Paint(r viewspec.Role, s string) string { return r.String() + ":" + s }

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
	}, r.Lines, "whole lines, not matches — a traceback reads as a unit")
}

func TestJSON_IndentsWhatItCanAndPassesTheRestThrough(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "none"},
		Blocks: []viewspec.Block{{Kind: "json"}}}
	assert.Equal(t, []string{`{`, `  "b": 2`, `}`},
		draw(t, spec, `{"b":2}`, 40))
	assert.Equal(t, []string{"not json at all"},
		draw(t, spec, "not json at all\n", 40), "invalid JSON is still shown")
}

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

func TestColumnOrder_SurvivesSkip(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "columns", Header: true, Skip: 1},
		Blocks: []viewspec.Block{{Kind: "table"}}}
	got := draw(t, spec, "banner line\nZEBRA  ALPHA\n1      2\n", 40)
	require.Len(t, got, 2)
	assert.Equal(t, "ZEBRA ALPHA", strings.Join(strings.Fields(got[0]), " "),
		"the skip wrapper forwards ColumnOrder rather than swallowing it")
}

func TestLinesParse_OrdersFieldsByCapture(t *testing.T) {
	spec := viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{{Kind: "table"}}}
	got := draw(t, spec, goTest, 60)
	require.NotEmpty(t, got)
	assert.Equal(t, "status pkg secs", strings.Join(strings.Fields(got[0]), " "),
		"left to right through the pattern")
}

// Navigable and actionable are different: the cursor moves through any
// row widget, but enter only resolves where on_enter says what to run.
func TestAction_NeedsOnEnterEvenWhenNavigable(t *testing.T) {
	spec := viewspec.Spec{Parse: linesParse(),
		Blocks: []viewspec.Block{{Kind: "list", Field: "pkg"}}}
	b := bind(t, spec, goTest)

	n, ok := b.SelectableRows()
	require.True(t, ok)
	assert.Equal(t, 4, n)

	_, ok = b.Action(viewspec.Frame{Cursor: 0})
	assert.False(t, ok, "nothing to activate")
}

// Row keys are lowercased so a spec can name them predictably; the
// header a human reads keeps whatever the output called it.
func TestColumns_KeysAreLowerAndTitlesAreNot(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "columns", Header: true},
		Blocks: []viewspec.Block{
			{Kind: "table"},
			{Kind: "list", Field: "user", OnEnter: "id {user}"},
		}}
	b := bind(t, spec, "USER PID\nroot 1\n")

	r, err := b.Draw(viewspec.Frame{Width: 40, Paint: viewspec.Plain()})
	require.NoError(t, err)
	assert.Contains(t, r.Lines[0], "USER", "the header is the output's")

	got, ok := b.Action(viewspec.Frame{Cursor: 0})
	require.True(t, ok, "while the spec addresses the lowercased key")
	assert.Equal(t, "id root", got)
}

// With one cursor for the whole view, the block that can be acted on
// gets it — however the blocks are ordered.
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

func TestExtract_ShapesBeyondWhitespaceColumns(t *testing.T) {
	tests := []struct {
		name   string
		parse  viewspec.Parse
		output string
		want   []viewspec.Row
	}{
		{
			name:  "fixed reads multi-word headings",
			parse: viewspec.Parse{Kind: "fixed"},
			output: "CONTAINER ID   IMAGE     STATUS\n" +
				"a1b2c3d4e5f6   nginx     Up 3 hours\n",
			want: []viewspec.Row{
				{"container id": "a1b2c3d4e5f6", "image": "nginx", "status": "Up 3 hours"},
			},
		},
		{
			name:   "pairs splits on a separator",
			parse:  viewspec.Parse{Kind: "pairs", Sep: "="},
			output: "HOME=/Users/mo\nSHELL=/bin/zsh\n",
			want: []viewspec.Row{
				{"key": "HOME", "value": "/Users/mo"},
				{"key": "SHELL", "value": "/bin/zsh"},
			},
		},
		{
			name:   "delimited reads a colon-separated file",
			parse:  viewspec.Parse{Kind: "delimited", Sep: ":", Fields: []string{"user", "x", "uid"}},
			output: "root:*:0\ndaemon:*:1\n",
			want: []viewspec.Row{
				{"user": "root", "x": "*", "uid": "0"},
				{"user": "daemon", "x": "*", "uid": "1"},
			},
		},
		{
			name:   "indent turns whitespace into levels",
			parse:  viewspec.Parse{Kind: "indent"},
			output: "src\n  main.go\n  ui\n    view.go\n",
			want: []viewspec.Row{
				{"depth": "0", "text": "src"},
				{"depth": "1", "text": "main.go"},
				{"depth": "1", "text": "ui"},
				{"depth": "2", "text": "view.go"},
			},
		},
		{
			name:   "indent normalises tabs to the same levels",
			parse:  viewspec.Parse{Kind: "indent"},
			output: "src\n\tmain.go\n\t\tdeep.go\n",
			want: []viewspec.Row{
				{"depth": "0", "text": "src"},
				{"depth": "1", "text": "main.go"},
				{"depth": "2", "text": "deep.go"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := viewspec.Spec{Parse: tc.parse, Blocks: []viewspec.Block{{Kind: "log"}}}
			got, err := rowsOf(spec, tc.output)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParse_SepIsRequiredWhereItIsTheWholePoint(t *testing.T) {
	for _, kind := range []string{"pairs", "delimited"} {
		_, err := viewspec.Compile(viewspec.Spec{
			Parse:  viewspec.Parse{Kind: kind, Header: true},
			Blocks: []viewspec.Block{{Kind: "log"}}})
		assert.Error(t, err, kind)
	}
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

func TestSparkline_ScalesToTheValuesPresent(t *testing.T) {
	spec := viewspec.Spec{Parse: linesParse(),
		Blocks: []viewspec.Block{{Kind: "sparkline", Field: "secs", Title: "took"}}}
	got := draw(t, spec, goTest, 60)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "took ")
	assert.Contains(t, got[0], "0.412–10.2", "the range it scaled against")
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
	// 10.200, 9.500, 1.203, 0.412 — descending, and the largest fills.
	assert.Greater(t, bars[0], bars[1])
	assert.Greater(t, bars[1], bars[2])
	assert.Greater(t, bars[2], bars[3])
	assert.InDelta(t, 9.5/10.2, float64(bars[1])/float64(bars[0]), 0.05,
		"bar length tracks the value")
}

// Every built-in says what it is for and what it is not, because a
// fourteen-way choice on flat one-liners loses a model's calibration.
func TestSchema_EveryBuiltinDescribesItself(t *testing.T) {
	reg := viewspec.Standard()
	guide := reg.Schema()["properties"].(map[string]any)["widget_guide"].(map[string]any)
	got := guide["const"].(map[string]viewspec.Description)

	for _, kind := range reg.Kinds() {
		d, ok := got[kind]
		require.True(t, ok, "%s describes itself", kind)
		assert.NotEmpty(t, d.What, kind)
		assert.NotEmpty(t, d.NotFor, "%s names what it is confused with", kind)
		assert.NotEmpty(t, d.Examples, kind)
	}

	// A widget that says nothing is absent rather than blank.
	require.NoError(t, reg.Widget("hologram", viewspec.WidgetFunc(
		func(viewspec.Block, viewspec.Data, viewspec.Frame) ([]string, error) { return nil, nil })))
	guide = reg.Schema()["properties"].(map[string]any)["widget_guide"].(map[string]any)
	assert.NotContains(t, guide["const"].(map[string]viewspec.Description), "hologram")
}
