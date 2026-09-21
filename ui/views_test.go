package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

const goTestOutput = `ok  	github.com/x/a	0.412s
FAIL	github.com/x/b	1.203s
ok  	github.com/x/c	9.500s
`

// rowFor builds a finished command row focused in the output pane,
// which is the only state a view is ever drawn in.
func rowFor(command, stdout string) Model {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{{
		command: command,
		cmd: cmdState{ec: &ExecutedCommand{
			Command: command,
			Result:  Result{Stdout: stdout},
			Post:    &PostJudgment{RenderKind: KindLog},
		}},
	}}}}
	m.nav.focus = focusOutput
	m.nav.cursor = 0
	return m
}

func plainLines(m Model) []string {
	lines := m.detailLines()
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimRight(ansi.Strip(l), " ")
	}
	return out
}

func TestViews_GoTestRendersThroughViewspec(t *testing.T) {
	got := plainLines(rowFor("go test ./...", goTestOutput))
	require.NotEmpty(t, got)

	assert.Contains(t, got[0], "passed", "the meter leads")
	assert.Contains(t, got[0], "2/3", "counted from rows, not from prose")
	assert.Contains(t, got[1], "package", "then the table header")
	assert.Equal(t, "ok     github.com/x/c 9.500", got[2],
		"sorted by duration, descending and numeric")
	assert.Equal(t, "FAIL   github.com/x/b 1.203", got[3])
}

func TestViews_GitStatusRendersThroughViewspec(t *testing.T) {
	got := plainLines(rowFor("git status --porcelain=v1 --branch",
		"## main...origin/main\n M ui/model.go\n?? viewspec/\n"))
	require.Len(t, got, 3)
	assert.Equal(t, " M 1  ?? 1", got[0], "badges summarise the codes")
	assert.NotContains(t, strings.Join(got, "\n"), "origin/main",
		"the --branch header is not a file")
	assert.Equal(t, "ui/model.go", got[1])
	assert.Equal(t, "viewspec/", got[2])
}

func TestViews_PsRendersThroughViewspec(t *testing.T) {
	got := plainLines(rowFor("ps -U someone",
		"  PID TTY           TIME CMD\n  501 ttys000    0:00.412 -zsh\n  622 ttys001    0:01.003 vim\n"))
	require.Len(t, got, 3)
	assert.Contains(t, got[0], "command", "the header renames cmd")
	assert.Contains(t, got[1], "501")
	assert.Contains(t, got[2], "vim")
}

// A view is a lens, never a replacement: anything that doesn't resolve
// leaves the render_kind path exactly as it was.
func TestViews_FallBackRatherThanRenderWrong(t *testing.T) {
	tests := []struct {
		name    string
		command string
		stdout  string
	}{
		{"no spec for this command", "curl https://example.com", "hello\n"},
		{"spec exists but the output does not match", "go test ./...", "no packages found\n"},
		{"a multiplexer subcommand with no spec", "go build ./...", "some output\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := rowFor(tc.command, tc.stdout)
			_, ok := m.viewLines()
			assert.False(t, ok, "must fall through to render_kind")
			assert.NotEmpty(t, m.detailLines(), "the pane still draws")
		})
	}
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

func TestViews_ActionSeedsACommand(t *testing.T) {
	m := rowFor("go test ./...", goTestOutput)
	row := m.blocks[0].steps[0]
	b, ok := boundView(row)
	require.True(t, ok)

	got, ok := b.Action(viewspec.Frame{Cursor: 0})
	require.True(t, ok)
	assert.Equal(t, "go test -v github.com/x/c", got, "the slowest package, after the sort")
}

func TestNormaliseCommand(t *testing.T) {
	tests := []struct{ command, want string }{
		{"go test ./...", "go test"},
		{"go test -run TestX ./internal/agent", "go test"},
		{"git status --porcelain=v1 --branch", "git status"},
		{"git status -sb", "git status"},
		{"ps -U someone", "ps"},
		{"ps", "ps"},
		{"ls -la", "ls"},
		{"docker ps -a", "docker ps"},
		{"", ""},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, normaliseCommand(tc.command), tc.command)
	}
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

func TestHandWrittenSpecs_AllCompile(t *testing.T) {
	for key, spec := range handWritten {
		_, err := viewspec.Compile(spec, viewspec.WithRegistry(viewRegistry))
		assert.NoError(t, err, key)
	}
	assert.Len(t, compiled, len(handWritten), "every hand-written spec compiled")
}
