package viewspec_test

import (
	"encoding/json"
	"errors"
	"slices"
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
// all. That is the reason Painter lives on Frame.
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

// rolePainter makes roles visible to assertions. Plain paints nothing,
// on purpose, so golden files stay readable.
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

// "Mounted on" names one column more than df prints, so the one row
// kept was the misaligned one, drawn wrong in every cell.
func TestColumns_AMisreadHeaderFailsRatherThanKeepingTheRowsItFits(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "columns", Header: true},
		Blocks: []viewspec.Block{{Kind: "table"}}}
	c, err := viewspec.Compile(spec)
	require.NoError(t, err)
	_, err = c.Bind(dfH)
	assert.ErrorContains(t, err, "7 of 8 lines have fewer fields")

	// One short line is a total or a footer, not a misread header.
	_, err = c.Bind("NAME SIZE\na 1\nb 2\nc 3\ntotal\n")
	assert.NoError(t, err)
}

const dfH = "Filesystem      Size  Used Avail Use% Mounted on\n" +
	"/dev/disk3s1s1  926G   10G  560G   2% /\n" +
	"devfs           205K  205K    0B 100% /dev\n" +
	"/dev/disk3s6    926G  7.0G  560G   2% /System/Volumes/VM\n" +
	"/dev/disk3s2    926G  7.6G  560G   2% /System/Volumes/Preboot\n" +
	"/dev/disk3s4    926G  3.1M  560G   1% /System/Volumes/Update\n" +
	"/dev/disk1s2    500M  6.0M  483M   2% /System/Volumes/xarts\n" +
	"/dev/disk3s5    926G  345G  560G  39% /System/Volumes/Data\n" +
	"map auto_home     0B    0B    0B 100% /System/Volumes/Data/home\n"

// Cut at the header's titles, the right-aligned 926Gi under "Size"
// read as 9 and 26Gi, and single-spaced titles merged into one field.
func TestFixed_KeepsAValueWiderThanItsTitleWhole(t *testing.T) {
	const macDF = "Filesystem        Size    Used   Avail Capacity iused ifree %iused  Mounted on\n" +
		"/dev/disk3s1s1   926Gi    12Gi   352Gi     4%    459k  3.7G    0%   /\n" +
		"devfs            222Ki   222Ki     0Bi   100%     768     0  100%   /dev\n" +
		"/dev/disk3s6     926Gi    16Gi   352Gi     5%      15  3.7G    0%   /System/Volumes/VM\n"
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "fixed"},
		Blocks: []viewspec.Block{{Kind: "list", Field: "size"}}}
	b := bind(t, spec, macDF)
	assert.Equal(t, []string{"%iused", "avail", "capacity", "filesystem", "ifree", "iused",
		"mounted on", "size", "used"}, b.Fields())
	assert.Equal(t, viewspec.Row{"filesystem": "devfs", "size": "222Ki", "used": "222Ki",
		"avail": "0Bi", "capacity": "100%", "iused": "768", "ifree": "0", "%iused": "100%",
		"mounted on": "/dev"}, b.Sample(2)[1])

	// "Capacity" runs over the rows' gap and 6434718 over the header's,
	// so no column is blank on every line: the rows alone decide the cuts.
	const macDFk = "Filesystem     1024-blocks      Used Available Capacity iused      ifree %iused  Mounted on\n" +
		"/dev/disk3s1s1   971350180  12337596 369012392     4%  458732 3690123920    0%   /\n" +
		"devfs                  222       222         0   100%     768          0  100%   /dev\n" +
		"/dev/disk3s5     971350180 563019440 368956984    61% 6434718 3689569840    0%   /System/Volumes/Data\n"
	table := viewspec.Spec{Parse: viewspec.Parse{Kind: "fixed"}, Blocks: []viewspec.Block{{Kind: "table"}}}
	rows := bind(t, table, macDFk).Sample(3)
	assert.Equal(t, "4%", rows[0]["capacity"])
	assert.Equal(t, "458732", rows[0]["iused"])
	assert.Equal(t, "61%", rows[2]["capacity"])
	assert.Equal(t, "6434718", rows[2]["iused"])
}

// A title word with nothing under it joins its neighbour, before or
// after; one with no neighbour is a column nothing filled in.
func TestFixed_TitleWordsWithNothingUnderThem(t *testing.T) {
	table := viewspec.Spec{Parse: viewspec.Parse{Kind: "fixed"}, Blocks: []viewspec.Block{{Kind: "table"}}}
	images := "IMAGE          ID             DISK USAGE   CONTENT SIZE   EXTRA\n" +
		"alpine:3.20    a4f4213abb84       13.6MB         4.09MB        \n" +
		"busybox:1.36   b9598f8c98e2       6.14MB          1.9MB        \n"
	assert.Equal(t, []string{"content size", "disk usage", "extra", "id", "image"},
		bind(t, table, images).Fields())

	ps := "CONTAINER ID   IMAGE    STATUS                      PORTS     NAMES\n" +
		"0d4dd713bd0a   alpine   Exited (137) 7 days ago               web\n" +
		"0e37e1d21d10   pause    Up 2 hours                            db\n"
	b := bind(t, table, ps)
	assert.Equal(t, []string{"container id", "image", "names", "ports", "status"}, b.Fields())
	assert.Equal(t, "Exited (137) 7 days ago", b.Sample(1)[0]["status"])
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

	// A JSON document spreads a record over lines; counting them means nothing.
	doc := viewspec.Spec{Parse: viewspec.Parse{Kind: "json"}, Blocks: []viewspec.Block{{Kind: "table"}}}
	assert.False(t, bind(t, doc, "[\n  {\n    \"id\": 1\n  },\n  {\n    \"id\": 2\n  }\n]\n").Hides())
}

func TestRegistry_SelectsIsTheKindsThatDrawARowPerLine(t *testing.T) {
	reg := viewspec.Standard()
	assert.True(t, reg.Selects("list"))
	assert.True(t, reg.Selects("table"))
	assert.False(t, reg.Selects("histogram"))
	assert.False(t, reg.Selects("nonsense"))
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

// A long free-text column must not cost a short one its digits.
func TestTable_NarrowColumnsKeepTheirWidth(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "columns", Header: true},
		Blocks: []viewspec.Block{{Kind: "table"}}}
	got := draw(t, spec, "PID RSS COMMAND\n77612 450123 "+strings.Repeat("x", 200)+"\n", 60)
	require.Len(t, got, 2)
	assert.True(t, strings.HasPrefix(got[1], "77612 450123 x"), "got %q", got[1])
	assert.True(t, strings.HasSuffix(got[1], "…"), "the long column is the one cut")
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
	// 10.200, 9.500, 1.203, 0.412: descending, and the largest fills.
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

// The schema spells a pane's blocks out rather than pointing back at
// itself, because a recursive $ref is where strict mode gets thin.
func TestSchema_PanesAreFiniteAndCannotNest(t *testing.T) {
	blocks := viewspec.Standard().Schema()["properties"].(map[string]any)["blocks"].(map[string]any)
	props := blocks["items"].(map[string]any)["properties"].(map[string]any)

	inner := props["panes"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["blocks"].(map[string]any)["items"].(map[string]any)
	assert.NotContains(t, inner["properties"].(map[string]any), "panes",
		"a pane's blocks have no panes of their own")
	assert.NotContains(t, inner["properties"].(map[string]any)["kind"].(map[string]any)["enum"],
		viewspec.RowKind, "and cannot be a row")
	assert.Contains(t, props["kind"].(map[string]any)["enum"], viewspec.RowKind,
		"while a top-level block still can")
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

// A kind that cannot report a cursor cannot act on a row either, and
// saying so at compile time beats a spec that looks right and does
// nothing when the human presses enter.
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

func twoCols(a, b string) []viewspec.Column {
	return []viewspec.Column{{Field: a}, {Field: b}}
}

func threeCols(a, b, c string) []viewspec.Column {
	return []viewspec.Column{{Field: a}, {Field: b}, {Field: c}}
}

func TestSubset_NarrowsWidgetsAndValidatesAgainstTheSame(t *testing.T) {
	reg := viewspec.Standard().Subset("list", "meter", "nope")
	assert.Equal(t, []string{"list", "meter"}, reg.Kinds())
	assert.Equal(t, viewspec.Standard().ParseKinds(), reg.ParseKinds(),
		"parse kinds are not what gets narrowed")

	spec := viewspec.Spec{Parse: linesParse(),
		Blocks: []viewspec.Block{{Kind: "table", Columns: []viewspec.Column{{Field: "pkg"}}}}}
	_, err := viewspec.Compile(spec, viewspec.WithRegistry(reg))
	assert.Error(t, err, "a kind outside the subset will not compile")

	guide := reg.Schema()["properties"].(map[string]any)["widget_guide"].(map[string]any)
	assert.Len(t, guide["const"].(map[string]viewspec.Description), 2,
		"and the model is only told about what it may use")
}

// A kind that cannot be drawn without a field says so, because a
// model told to fill every key and leave unused ones empty will
// otherwise empty a required one and the block fails to bind.
//
// Slots are also what a caller composes from, so the count decides the
// shape of the block: one fills Field, more than one fills Columns in
// order. A kind needing something that is not a field declares none.
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

// stacked is a layout the package has never heard of, registered from
// outside it. It exists to prove the interpreter asks the widget what
// it is rather than knowing "row" and "panel" by name.
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
	assert.False(t, slices.Contains(nestedKinds(t, reg), "stacked"),
		"a container cannot sit inside a pane")
	_, err = viewspec.Compile(viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{
		{Kind: "stacked", Panes: []viewspec.Pane{{Blocks: []viewspec.Block{{Kind: "log"}}}}}}},
		viewspec.WithRegistry(reg))
	assert.ErrorContains(t, err, "stacked needs two panes", "its own Accept decides the shape")
}

// nestedKinds is what a pane may hold, per the schema.
func nestedKinds(t *testing.T, reg *viewspec.Registry) []string {
	t.Helper()
	blocks := reg.Schema()["properties"].(map[string]any)["blocks"].(map[string]any)
	panes := blocks["items"].(map[string]any)["properties"].(map[string]any)["panes"].(map[string]any)
	inner := panes["items"].(map[string]any)["properties"].(map[string]any)["blocks"].(map[string]any)
	kinds := inner["items"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)
	return kinds["enum"].([]string)
}

// One object per line is what jq -c, docker inspect and most
// structured logs print. Whole-document parsing rejects it, and the
// parse-kind spike found that out by choosing json correctly for
// output our own extractor then refused.
func TestJSON_ReadsOneObjectPerLine(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "json"},
		Blocks: []viewspec.Block{{Kind: "table"}}}

	got := draw(t, spec, "{\"a\":1,\"b\":\"two\"}\n{\"a\":2,\"b\":\"three\"}\n", 40)
	require.Len(t, got, 3, "a header and two rows")
	assert.Contains(t, got[1], "two")
	assert.Contains(t, got[2], "three")

	// A whole document still wins, and half a stream still fails.
	got = draw(t, spec, `[{"a":1},{"a":2}]`, 40)
	assert.Len(t, got, 3)

	c, err := viewspec.Compile(spec)
	require.NoError(t, err)
	_, err = c.Bind("{\"a\":1}\n{\"a\":2\n")
	assert.Error(t, err, "a truncated stream fails rather than drawing the half it liked")
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

// prefix exists because the composition spike found nine of fifteen
// real commands had no header and no separator, only a leading token
// and a remainder. Their alternative was a generated regexp, which is
// the slow, error-prone half of the pipeline.
func TestPrefix_TakesTheFirstTokenAndTheRest(t *testing.T) {
	tests := []struct {
		name, output string
		want         []viewspec.Row
	}{
		{
			name:   "git log --oneline",
			output: "3416b00 Let the header answer settle the parse kind\nda6986a Spike: show field values\n",
			want: []viewspec.Row{
				{"first": "3416b00", "rest": "Let the header answer settle the parse kind"},
				{"first": "da6986a", "rest": "Spike: show field values"},
			},
		},
		{
			name:   "du -sh, tab separated",
			output: "100K\tinternal/agent\n 16K\tlogging\n",
			want: []viewspec.Row{
				{"first": "100K", "rest": "internal/agent"},
				{"first": "16K", "rest": "logging"},
			},
		},
		{
			name:   "wc -l, right aligned",
			output: "     117 viewspec/spec.go\n     514 viewspec/view.go\n",
			want: []viewspec.Row{
				{"first": "117", "rest": "viewspec/spec.go"},
				{"first": "514", "rest": "viewspec/view.go"},
			},
		},
		{
			name:   "a single token, and a blank line",
			output: "FAIL\n\nok\n",
			want:   []viewspec.Row{{"first": "FAIL", "rest": ""}, {"first": "ok", "rest": ""}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := bind(t, viewspec.Spec{Parse: viewspec.Parse{Kind: "prefix"},
				Blocks: []viewspec.Block{{Kind: "table"}}}, tc.output)
			assert.Equal(t, tc.want, b.Sample(10))
			assert.Equal(t, []string{"first", "rest"}, fieldOrder(b),
				"the leading token comes first, as the output printed it")
		})
	}
}

// fieldOrder is the order a table would draw, which is the extractor's
// own rather than alphabetical.
func fieldOrder(b *viewspec.Bound) []string {
	r, err := b.Draw(viewspec.Frame{Width: 60, Paint: viewspec.Plain()})
	if err != nil {
		return nil
	}
	return strings.Fields(r.Lines[0])
}

// Split on whitespace, every border in a drawn table was a column of
// its own and every rule a row.
func TestBox_ReadsTablesDrawnWithBorders(t *testing.T) {
	tests := map[string]string{
		"mysql":    "+----+------+\n| id | name |\n+----+------+\n| 1  | ada  |\n| 2  |      |\n+----+------+\n2 rows in set (0.01 sec)\n",
		"psql":     " id | name\n----+------\n  1 | ada\n  2 | \n(2 rows)\n",
		"box":      "┌────┬──────┐\n│ id │ name │\n├────┼──────┤\n│ 1  │ ada  │\n│ 2  │      │\n└────┴──────┘\n",
		"markdown": "| id | name |\n|----|------|\n| 1  | ada  |\n| 2  |      |\n",
	}
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "box"}, Blocks: []viewspec.Block{{Kind: "table"}}}
	for name, output := range tests {
		t.Run(name, func(t *testing.T) {
			b := bind(t, spec, output)
			assert.Equal(t, []string{"id", "name"}, b.Fields())
			assert.Equal(t, []viewspec.Row{{"id": "1", "name": "ada"}, {"id": "2", "name": ""}}, b.Sample(5))
		})
	}
}

// pip list rules its header off with dashes, which read as a package.
func TestColumns_SkipsARuleUnderTheHeader(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "columns", Header: true}, Blocks: []viewspec.Block{{Kind: "table"}}}
	b := bind(t, spec, "Package Version\n------- -------\nnumpy   2.1.1\n")
	assert.Equal(t, []viewspec.Row{{"package": "numpy", "version": "2.1.1"}}, b.Sample(5))
}

// Split on the comma alone, a quoted "Smith, John" was two cells and
// the rest of its row shifted right, with nothing to show it had.
func TestDelimited_KeepsAQuotedSeparatorInItsCell(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "delimited", Header: true, Sep: ","},
		Blocks: []viewspec.Block{{Kind: "table"}}}
	b := bind(t, spec, "name,company,city\n\"Smith, John\",\"Acme, Inc.\",London\nAda,Initech,York\n")
	assert.Equal(t, viewspec.Row{"name": "Smith, John", "company": "Acme, Inc.", "city": "London"}, b.Sample(1)[0])
}
