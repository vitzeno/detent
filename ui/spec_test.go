package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/theme"
	"github.com/vitzeno/detent/viewspec"
)

// web_search output is markdown from a reader, so links and emphasis
// are worth drawing rather than showing as their source.
func TestFallbackChain_AToolThatDeclaresMarkdownGetsIt(t *testing.T) {
	const searchOutput = "Title: containerd at DuckDuckGo\n\nMarkdown Content:\n" +
		"1.[containerd docs](https://duckduckgo.com/l/?uddg=https%3A%2F%2Fcontainerd.io)\n" +
		"An open and reliable **container** runtime.\n"

	declared := &historyRow{command: "web_search query=containerd", renders: event.RendersMarkdown,
		result: &event.Result{Stdout: searchOutput}}
	assert.Same(t, compiledMarkdown, fallbackChain(declared, searchOutput)[0],
		"a declared markdown shape did not reach the markdown view")

	// The same bytes with nothing declared stay on the plain path,
	// since the body does not open like markdown.
	plain := &historyRow{command: "bash", result: &event.Result{Stdout: searchOutput}}
	assert.NotSame(t, compiledMarkdown, fallbackChain(plain, searchOutput)[0])
}

// An edit declares its output a diff, so it is drawn as one even when
// the judge has not looked.
func TestFallbackChain_AnEditIsDrawnAsADiff(t *testing.T) {
	const out = "--- f.go\n+++ f.go\n@@ -1 +1 @@\n-a\n+b\n"
	r := &historyRow{command: "edit_file path=f.go", renders: event.RendersDiff, result: &event.Result{Stdout: out}}
	assert.Same(t, compiledFallback["diff"], fallbackChain(r, out)[0])
}

// A judged kind must not override what the tool actually knows.
func TestFallbackChain_ADeclaredShapeBeatsAJudgedGuess(t *testing.T) {
	r := &historyRow{
		command: "web_search query=containerd", renders: event.RendersMarkdown,
		result: &event.Result{Stdout: "Title: x\n"},
		post:   &verdict{renderKind: "plain_text", fromJudge: true},
	}
	assert.Same(t, compiledMarkdown, fallbackChain(r, "Title: x\n")[0],
		"the judge's guess overrode the tool's own answer")
}

// git branch indents every branch but the current one, and the listing
// view's pattern skips indented lines: it drew one branch of four.
func TestBoundView_SkipsAViewThatHidesMostOfTheOutput(t *testing.T) {
	const branches = "  agentic\n  dynamic-tui\n* main\n  mouse-scroll\n"
	r := &historyRow{command: "git branch", result: &event.Result{Stdout: branches},
		post: &verdict{renderKind: "file_listing", fromJudge: true}}
	b, ok := boundView(r)
	require.True(t, ok)
	assert.False(t, b.Hides())
	assert.Equal(t, "built-in", r.viewSource)
	assert.Contains(t, strings.Join(drawPlain(t, b), "\n"), "mouse-scroll")
}

// compileAll drops a spec that fails quietly, so pin that none does:
// a nil floor would panic in fallbackChain.
func TestShippedSpecs_AllCompile(t *testing.T) {
	assert.Len(t, compiledFallback, len(byKind()))
	assert.NotNil(t, compiledMarkdown)
	assert.NotNil(t, compiledPlain)
}

// One widget serves every row, so two documents must not evict each
// other, and a theme change must not serve the old colours.
func TestMarkdownWidget_CachesPerDocumentAndStyle(t *testing.T) {
	w := &markdownWidget{}
	f := viewspec.Frame{Width: 60}
	a, err := w.Draw(viewspec.Block{}, viewspec.Data{Raw: "# A\n\nfirst"}, f)
	require.NoError(t, err)
	_, err = w.Draw(viewspec.Block{}, viewspec.Data{Raw: "# B\n\nsecond"}, f)
	require.NoError(t, err)
	assert.Len(t, w.cache, 2)

	a[0] = "scribbled"
	again, err := w.Draw(viewspec.Block{}, viewspec.Data{Raw: "# A\n\nfirst"}, f)
	require.NoError(t, err)
	assert.NotEqual(t, "scribbled", again[0], "a caller cannot write into the cache")

	was := theme.Current()
	t.Cleanup(func() { theme.Apply(was) })
	theme.Apply(theme.Themes["light"])
	_, err = w.Draw(viewspec.Block{}, viewspec.Data{Raw: "# A\n\nfirst"}, f)
	require.NoError(t, err)
	assert.Len(t, w.cache, 3, "the style is part of the key")
}

// Draw runs inside View, so a widget that panics on odd output would
// take the TUI with it. The output is still worth showing as text.
func TestViewBody_APanickingViewFallsBackToText(t *testing.T) {
	_, evs := oneTurn("list", "ls", "a.go\nb.go")
	m := sized(t, 120, 40, evs...)
	r := m.rows()[0]

	reg := viewspec.Standard()
	require.NoError(t, reg.Widget("boom", panicWidget{}))
	c, err := viewspec.Compile(viewspec.Spec{Version: viewspec.Version,
		Parse: viewspec.Parse{Kind: "none"}, Blocks: []viewspec.Block{{Kind: "boom"}}},
		viewspec.WithRegistry(reg))
	require.NoError(t, err)
	b, err := c.Bind(r.text())
	require.NoError(t, err)
	r.view, r.viewTried = b, true

	var out viewspec.Render
	var ok bool
	require.NotPanics(t, func() { out, ok = m.viewBody(r) })
	assert.True(t, ok)
	assert.Equal(t, []string{"a.go", "b.go"}, out.Lines)
	assert.Equal(t, -1, out.CursorLine)
}

type panicWidget struct{}

func (panicWidget) Draw(viewspec.Block, viewspec.Data, viewspec.Frame) ([]string, error) {
	panic("strings: negative Repeat count")
}

func drawPlain(t *testing.T, b *viewspec.Bound) []string {
	t.Helper()
	out, err := b.Draw(viewspec.Frame{Width: 40})
	require.NoError(t, err)
	return out.Lines
}
