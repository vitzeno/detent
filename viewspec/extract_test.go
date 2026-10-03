package viewspec_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

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
			output: "HOME=/home/ada\nSHELL=/bin/zsh\n",
			want: []viewspec.Row{
				{"key": "HOME", "value": "/home/ada"},
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

func TestLinesParse_OrdersFieldsByCapture(t *testing.T) {
	spec := viewspec.Spec{Parse: linesParse(), Blocks: []viewspec.Block{{Kind: "table"}}}
	got := draw(t, spec, goTest, 60)
	require.NotEmpty(t, got)
	assert.Equal(t, "status pkg secs", strings.Join(strings.Fields(got[0]), " "),
		"left to right through the pattern")
}

func TestColumnOrder_SurvivesSkip(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "columns", Header: true, Skip: 1},
		Blocks: []viewspec.Block{{Kind: "table"}}}
	got := draw(t, spec, "banner line\nZEBRA  ALPHA\n1      2\n", 40)
	require.Len(t, got, 2)
	assert.Equal(t, "ZEBRA ALPHA", strings.Join(strings.Fields(got[0]), " "),
		"the skip wrapper forwards ColumnOrder rather than swallowing it")
}

// Row keys are lowercased so a spec can name them predictably, while the
// header keeps whatever the output called it.
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
	require.ErrorContains(t, err, "7 of 8 lines have fewer fields")

	// One short line is a total or a footer, not a misread header.
	_, err = c.Bind("NAME SIZE\na 1\nb 2\nc 3\ntotal\n")
	assert.NoError(t, err)
}

// pip list rules its header off with dashes, which read as a package.
func TestColumns_SkipsARuleUnderTheHeader(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "columns", Header: true}, Blocks: []viewspec.Block{{Kind: "table"}}}
	b := bind(t, spec, "Package Version\n------- -------\nnumpy   2.1.1\n")
	assert.Equal(t, []viewspec.Row{{"package": "numpy", "version": "2.1.1"}}, b.Sample(5))
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
// after. One with no neighbour is a column nothing filled in.
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

// A float64 would round an id past 2^53 and drop a fraction's digits.
func TestJSON_RowsKeepNumbersAsPrinted(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "json"}, Blocks: []viewspec.Block{{Kind: "table"}}}
	for _, output := range []string{
		`[{"id":12345678901234567890,"f":0.1234567,"tiny":1e-7,"ok":true}]`,
		`{"id":12345678901234567890,"f":0.1234567,"tiny":1e-7,"ok":true}`,
		"{\"id\":12345678901234567890,\"f\":0.1234567,\"tiny\":1e-7,\"ok\":true}\n",
	} {
		assert.Equal(t, []viewspec.Row{{"id": "12345678901234567890", "f": "0.1234567", "tiny": "1e-7", "ok": "true"}},
			bind(t, spec, output).Sample(1), output)
	}
}

// One object per line is what jq -c and most structured logs print.
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

// Split on the comma alone, a quoted "Smith, John" was two cells and
// the rest of its row shifted right, with nothing to show it had.
func TestDelimited_KeepsAQuotedSeparatorInItsCell(t *testing.T) {
	spec := viewspec.Spec{Parse: viewspec.Parse{Kind: "delimited", Header: true, Sep: ","},
		Blocks: []viewspec.Block{{Kind: "table"}}}
	b := bind(t, spec, "name,company,city\n\"Smith, John\",\"Acme, Inc.\",London\nAda,Initech,York\n")
	assert.Equal(t, viewspec.Row{"name": "Smith, John", "company": "Acme, Inc.", "city": "London"}, b.Sample(1)[0])
}

// prefix reads the common headerless shape: a leading token and a
// remainder, with no generated regexp needed.
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

const dfH = "Filesystem      Size  Used Avail Use% Mounted on\n" +
	"/dev/disk3s1s1  926G   10G  560G   2% /\n" +
	"devfs           205K  205K    0B 100% /dev\n" +
	"/dev/disk3s6    926G  7.0G  560G   2% /System/Volumes/VM\n" +
	"/dev/disk3s2    926G  7.6G  560G   2% /System/Volumes/Preboot\n" +
	"/dev/disk3s4    926G  3.1M  560G   1% /System/Volumes/Update\n" +
	"/dev/disk1s2    500M  6.0M  483M   2% /System/Volumes/xarts\n" +
	"/dev/disk3s5    926G  345G  560G  39% /System/Volumes/Data\n" +
	"map auto_home     0B    0B    0B 100% /System/Volumes/Data/home\n"

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

// fieldOrder is the order a table would draw, which is the extractor's
// own rather than alphabetical.
func fieldOrder(b *viewspec.Bound) []string {
	r, err := b.Draw(viewspec.Frame{Width: 60, Paint: viewspec.Plain()})
	if err != nil {
		return nil
	}
	return strings.Fields(r.Lines[0])
}
