package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

const goTestOutput = `ok  	github.com/x/a	0.412s
FAIL	github.com/x/b	1.203s
ok  	github.com/x/c	9.500s
`

// goTestSpec stands in for whatever the Driver hands back, generated
// or saved. ui never authors one.
func goTestSpec() viewspec.Spec {
	return viewspec.Spec{
		Version: viewspec.Version,
		Match:   "go test",
		Parse: viewspec.Parse{Kind: "lines",
			Pattern: `^(?P<status>ok|FAIL)\s+(?P<pkg>\S+)\s+(?P<secs>[\d.]+)s`},
		Blocks: []viewspec.Block{
			{Kind: "meter", Title: "passed", CountWhere: "status=ok", Of: "*"},
			{Kind: "table",
				Columns: []viewspec.Column{
					{Field: "status"}, {Field: "pkg", Title: "package"}, {Field: "secs", Title: "took"}},
				Sort: &viewspec.Sort{Field: "secs", Numeric: true, Desc: true},
				Accent: &viewspec.Accent{Field: "status", Map: map[string]viewspec.Role{
					"ok": viewspec.RoleSafe, "FAIL": viewspec.RoleDanger}},
				OnEnter: "go test -v {pkg}"},
		},
	}
}

func rowFor(command, stdout string) Model {
	return rowForKind(command, stdout, KindText)
}

func rowForKind(command, stdout string, kind RenderKind) Model {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{{
		command: command,
		cmd: cmdState{ec: &ExecutedCommand{
			Command: command,
			Result:  Result{Stdout: stdout},
			Post:    &PostJudgment{RenderKind: kind},
		}},
	}}}}
	m.nav.focus = focusOutput
	m.nav.cursor = 0
	return m
}

// rowWithView is a row the Driver has already supplied a view for.
func rowWithView(command, stdout string, spec viewspec.Spec) Model {
	return rowWithSourcedView(command, stdout, spec, ViewGenerated)
}

func rowWithSourcedView(command, stdout string, spec viewspec.Spec, src ViewSource) Model {
	m := rowFor(command, stdout)
	applyView(m.blocks[0].steps[0], GeneratedView{Spec: &spec, Source: src})
	return m
}

// plainLines is what the pane actually shows, stripped of colour. The
// viewport pads to its own height, so trailing blanks are dropped.
func plainLines(m Model) []string {
	m.refreshViewport()
	var out []string
	for _, l := range m.detailLines() {
		out = append(out, strings.TrimRight(ansi.Strip(l), " "))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func TestViews_ADriverSpecDrawsThePane(t *testing.T) {
	got := plainLines(rowWithView("go test ./...", goTestOutput, goTestSpec()))
	require.NotEmpty(t, got)

	assert.Contains(t, got[0], "passed", "the meter leads")
	assert.Contains(t, got[0], "2/3", "counted from rows, not from prose")
	assert.Contains(t, got[1], "package", "then the table header")
	assert.Equal(t, "ok     github.com/x/c 9.500", got[2],
		"sorted by duration, descending and numeric")
}

// The Driver is asked once a row is judged, because render_kind is
// what prunes the vocabulary a view may be drawn from.
func TestViews_GenerationIsAskedForOnceAndOffTheUpdateLoop(t *testing.T) {
	m := rowFor("go test ./...", goTestOutput)
	row := m.blocks[0].steps[0]
	spec := goTestSpec()
	m.sess.(*fakeDriver).generated = &spec

	cmd := m.generateView(row)
	require.NotNil(t, cmd, "asking happens in a tea.Cmd, not in Update")
	assert.Nil(t, m.generateView(row), "and only once per row")

	msg, ok := cmd().(viewMsg)
	require.True(t, ok)
	require.True(t, applyView(msg.row, msg.view))
	assert.Contains(t, plainLines(m)[0], "passed", "the pane upgrades in place")
}

// Until one arrives, and if none ever does, the render_kind fallback
// is what the pane shows.
func TestViews_FallBackRatherThanRenderWrong(t *testing.T) {
	tests := []struct {
		name    string
		command string
		stdout  string
		kind    RenderKind
		want    []string
	}{
		{"nothing generated yet", "curl https://example.com", "hello\n", KindText,
			[]string{"hello"}},
		{"json indents", "curl /api", `{"b":2,"a":1}`, KindJSON,
			[]string{"{", `  "a": 1,`, `  "b": 2`, "}"}},
		{"diff keeps its markers", "git diff", "+a\n-b\n", KindDiff,
			[]string{"+a", "-b"}},
		{"an unknown kind shows the bytes", "whatever", "plain\n", "unknown-kind",
			[]string{"plain"}},
		{"nothing judged yet shows the bytes", "whatever", "plain\n", "",
			[]string{"plain"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, plainLines(rowForKind(tc.command, tc.stdout, tc.kind)))
		})
	}
}

// A generated spec that cannot draw this output leaves the fallback
// exactly as it was.
func TestViews_ABrokenSpecChangesNothing(t *testing.T) {
	m := rowFor("go test ./...", goTestOutput)
	row := m.blocks[0].steps[0]
	before := plainLines(m)

	assert.False(t, applyView(row, GeneratedView{Spec: &viewspec.Spec{Version: 1,
		Parse:  viewspec.Parse{Kind: "lines", Pattern: `^(?P<status>ok)`},
		Blocks: []viewspec.Block{{Kind: "list", Field: "nothing_here"}}}}))
	assert.Equal(t, before, plainLines(m))

	assert.False(t, applyView(row, GeneratedView{}))
	assert.Equal(t, before, plainLines(m))
}

func TestViews_TableFallbackStillParsesColumns(t *testing.T) {
	got := plainLines(rowForKind("ps aux",
		"USER PID COMMAND\nroot 1 init\nmo 4821 node server.js\n", KindTable))
	require.Len(t, got, 3)
	assert.Equal(t, "USER PID COMMAND", strings.Join(strings.Fields(got[0]), " "))
}

func TestViews_BindIsAttemptedOnce(t *testing.T) {
	m := rowFor("go test ./...", goTestOutput)
	row := m.blocks[0].steps[0]

	_, ok := boundView(row)
	require.True(t, ok)
	first := row.cmd.view
	require.NotNil(t, first)

	_, ok = boundView(row)
	require.True(t, ok)
	assert.Same(t, first, row.cmd.view, "Draw runs per frame; Bind must not")
}

func TestViews_EnterSeedsThePromptFromTheCursorRow(t *testing.T) {
	m := rowWithView("go test ./...", goTestOutput, goTestSpec())

	nm, _ := m.outputKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)

	assert.Equal(t, "go test -v github.com/x/c", m.prompt.Value(),
		"the slowest package, which is row 0 after the sort")
	assert.Equal(t, focusInput, m.nav.focus, "focus comes back to the prompt")
	assert.Equal(t, modeInput, m.mode, "nothing ran and no confirm was shown")
}

func TestViews_SelectionMovesWithinTheViewAndClamps(t *testing.T) {
	m := rowWithView("go test ./...", goTestOutput, goTestSpec())
	row := m.blocks[0].steps[0]

	nm, _ := m.outputKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = nm.(Model)
	assert.Equal(t, 1, row.cmd.tableCursor)

	nm, _ = m.outputKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, "go test -v github.com/x/b", nm.(Model).prompt.Value(),
		"the seeded command follows the cursor")

	for range 5 {
		nm, _ = m.outputKey(tea.KeyPressMsg{Code: tea.KeyDown})
		m = nm.(Model)
	}
	assert.Equal(t, 2, row.cmd.tableCursor, "clamped to the last row")

	for range 9 {
		nm, _ = m.outputKey(tea.KeyPressMsg{Code: tea.KeyUp})
		m = nm.(Model)
	}
	assert.Equal(t, 0, row.cmd.tableCursor, "and to the first")
}

func TestViews_EnterWithoutAnActionStillTogglesTheRow(t *testing.T) {
	m := rowFor("curl https://example.com", "hello\n")
	row := m.blocks[0].steps[0]
	require.False(t, row.cmd.expanded)

	nm, _ := m.outputKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	assert.True(t, row.cmd.expanded, "the old behaviour is untouched")
	assert.Empty(t, m.prompt.Value())
}

// v and space mean "look at this", enter means "act on it".
func TestViews_OnlyEnterActivates(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'v', Text: "v"}, {Code: ' ', Text: " "},
	} {
		m := rowWithView("go test ./...", goTestOutput, goTestSpec())
		row := m.blocks[0].steps[0]

		nm, _ := m.outputKey(key)
		m = nm.(Model)
		assert.Empty(t, m.prompt.Value(), key.Text)
		assert.True(t, row.cmd.expanded, key.Text)
	}
}

// A view taller than the pane scrolls like any other output, and the
// selection stays on screen.
func TestViews_TallViewScrollsAndFollowsTheCursor(t *testing.T) {
	var out strings.Builder
	for i := range 60 {
		fmt.Fprintf(&out, "ok  \tgithub.com/x/p%02d\t%d.000s\n", i, 60-i)
	}
	m := rowWithView("go test ./...", out.String(), goTestSpec())
	row := m.blocks[0].steps[0]
	m.refreshViewport()

	h := m.output.Height()
	require.Greater(t, m.output.TotalLineCount(), h, "the view outgrows the pane")
	assert.Equal(t, 0, m.output.YOffset(), "and starts at the top")

	for range h + 5 {
		nm, _ := m.outputKey(tea.KeyPressMsg{Code: tea.KeyDown})
		m = nm.(Model)
		m.refreshViewport()
	}
	assert.Positive(t, m.output.YOffset(), "the pane scrolled to follow the cursor")
	assertCursorVisible(t, m, row)

	for range 100 {
		nm, _ := m.outputKey(tea.KeyPressMsg{Code: tea.KeyUp})
		m = nm.(Model)
		m.refreshViewport()
	}
	assert.Equal(t, 0, row.cmd.tableCursor)
	assert.Equal(t, 0, m.output.YOffset(), "and back to the top")
	assert.Contains(t, plainLines(m)[1], "package",
		"the header is in sight again, not scrolled just off")
}

func assertCursorVisible(t *testing.T, m Model, row *stepRow) {
	t.Helper()
	rendered, ok := m.viewBody(row)
	require.True(t, ok)
	require.GreaterOrEqual(t, rendered.CursorLine, 0)
	assert.GreaterOrEqual(t, rendered.CursorLine, m.output.YOffset(),
		"the selection is not above the window")
	assert.Less(t, rendered.CursorLine, m.output.YOffset()+m.output.Height(),
		"nor below it")
}

func TestViews_ScrollingLeavesTheSelectionAlone(t *testing.T) {
	var out strings.Builder
	for i := range 60 {
		fmt.Fprintf(&out, "ok  \tgithub.com/x/p%02d\t%d.000s\n", i, 60-i)
	}
	m := rowWithView("go test ./...", out.String(), goTestSpec())
	row := m.blocks[0].steps[0]
	m.refreshViewport()

	nm, _ := m.outputKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	m = nm.(Model)
	assert.Equal(t, 0, row.cmd.tableCursor, "pgdn scrolls, it does not select")
}

// Registering a widget here is also what puts it in the schema a model
// is given, so the generator cannot drift from the interpreter.
func TestViewRegistry_CarriesWhatOnlyDetentCanDo(t *testing.T) {
	assert.Contains(t, viewRegistry.Kinds(), "markdown")
	assert.NotContains(t, viewspec.Standard().Kinds(), "markdown",
		"glamour cannot live in a stdlib-only package")

	blocks := viewRegistry.Schema()["properties"].(map[string]any)["blocks"].(map[string]any)
	kinds := blocks["items"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)["enum"]
	assert.Contains(t, kinds, "markdown")
}

// Every drawn pane says which spec produced it. An unmarked one left
// the human guessing which of four paths they were looking at, which
// is the whole complaint the mark answers.
func TestViews_ThePaneAlwaysSaysWhereItsFramingCameFrom(t *testing.T) {
	for _, src := range []ViewSource{ViewShipped, ViewSaved, ViewGenerated} {
		m := rowWithSourcedView("go test ./...", goTestOutput, goTestSpec(), src)
		header := ansi.Strip(m.viewportHeader())
		assert.Contains(t, header, viewSourceMark, string(src))
		assert.Contains(t, header, string(src))
		assert.Contains(t, header, "go test ./...", "the command still fits")
	}

	// The built-in rendering is a spec too, so it says so.
	m := rowFor("curl https://example.com", "hello\n")
	m.refreshViewport()
	header := ansi.Strip(m.viewportHeader())
	assert.Contains(t, header, viewSourceMark)
	assert.Contains(t, header, string(ViewBuiltin))
}
