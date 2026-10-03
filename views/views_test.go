package views_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/views"
	"github.com/vitzeno/detent/viewspec"
)

// Every shipped spec compiles against the standard vocabulary, or the
// only thing that notices is the pane going blank.
func TestShipped_EverySpecCompiles(t *testing.T) {
	for _, kind := range views.Kinds() {
		spec, ok := views.ForKind(kind)
		require.True(t, ok, kind)
		_, err := viewspec.Compile(spec)
		assert.NoError(t, err, "the spec for %s output", kind)
	}
	for _, command := range views.Commands() {
		spec, ok := views.ForCommand(command)
		require.True(t, ok, command)
		_, err := viewspec.Compile(*spec)
		assert.NoError(t, err, "the spec shipped for %s", command)
	}
}

// A command spec is filed under its own match, so a saved copy round
// trips to the same key.
func TestShipped_CommandSpecsAreFiledUnderTheirMatch(t *testing.T) {
	for _, command := range views.Commands() {
		spec, _ := views.ForCommand(command)
		assert.Equal(t, command, spec.Match, "%s should match its own key", command)
	}
}

// ForCommand hands back a copy. Two rows drawing the same shipped spec
// must not be able to edit each other's.
func TestForCommand_HandsBackACopy(t *testing.T) {
	first, ok := views.ForCommand("git status")
	require.True(t, ok)
	first.Match = "vandalised"
	first.Blocks[1].Title = "vandalised"
	first.Blocks[1].Sort.Desc = true
	first.Blocks[0].Accent.Map["??"] = viewspec.RoleDanger

	second, ok := views.ForCommand("git status")
	require.True(t, ok)
	assert.Equal(t, "git status", second.Match)
	assert.Empty(t, second.Blocks[1].Title)
	assert.False(t, second.Blocks[1].Sort.Desc)
	assert.Equal(t, viewspec.RoleMuted, second.Blocks[0].Accent.Map["??"])
	assert.Equal(t, viewspec.RoleMuted, second.Blocks[1].Accent.Map["??"], "nor the block sharing its map")

	kind, ok := views.ForKind("table")
	require.True(t, ok)
	kind.Blocks[0].Kind = "vandalised"
	again, _ := views.ForKind("table")
	assert.Equal(t, "table", again.Blocks[0].Kind)
}

// Every shape's spec binds a sample of that shape, so a spec that compiles
// but reads nothing is caught here rather than in viewgen.
func TestShipped_KindSpecsBindTheirShape(t *testing.T) {
	samples := map[string]string{
		"plain_text":      "starting\nready\n",
		"error_text":      "panic: boom\n",
		"diff":            "--- a\n+++ b\n@@ -1 +1 @@\n-x\n+y\n",
		"structured_json": `{"a":1}`,
		"file_content":    "package main\n",
		"table":           "NAME  SIZE\na     1\nb     2\n",
		"file_listing":    "./main.go\n./ui/keys.go\n",
	}
	assert.ElementsMatch(t, views.Kinds(), keys(samples), "a sample for every kind")
	for kind, output := range samples {
		spec, _ := views.ForKind(kind)
		c, err := viewspec.Compile(spec)
		require.NoError(t, err, kind)
		b, err := c.Bind(output)
		require.NoError(t, err, kind)
		r, err := b.Draw(viewspec.Frame{Width: 40, Paint: viewspec.Plain()})
		require.NoError(t, err, kind)
		assert.NotEmpty(t, r.Lines, kind)
	}
}

// find prints depth first already, and sorting by bytes put src-old
// between src and src/a, which drew a under the wrong parent.
func TestFind_KeepsTheTreeFindPrinted(t *testing.T) {
	spec, ok := views.ForCommand("find")
	require.True(t, ok)
	c, err := viewspec.Compile(*spec)
	require.NoError(t, err)
	b, err := c.Bind("src\nsrc/a\nsrc-old\n")
	require.NoError(t, err)
	r, err := b.Draw(viewspec.Frame{Width: 40, Paint: viewspec.Plain()})
	require.NoError(t, err)
	assert.Equal(t, []string{"src", "└─ a", "src-old"}, r.Lines)
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Raw is the floor of every fallback chain: the bytes as they came,
// through one widget, parsing nothing.
func TestRaw_DrawsTheOutputAsItCame(t *testing.T) {
	c, err := viewspec.Compile(views.Raw("log"))
	require.NoError(t, err)
	b, err := c.Bind("one\ntwo\n")
	require.NoError(t, err)
	got, err := b.Draw(viewspec.Frame{Width: 20, Paint: viewspec.Plain()})
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, got.Lines)
}

// Most of a repeated go test run is cached, and a pattern that wanted
// a time hid every such package.
func TestGoTest_ReadsCachedPackages(t *testing.T) {
	spec, ok := views.ForCommand("go test")
	require.True(t, ok)
	c, err := viewspec.Compile(*spec)
	require.NoError(t, err)
	b, err := c.Bind("ok  \tgithub.com/x/a\t(cached)\nok  \tgithub.com/x/b\t0.412s\n" +
		"?   \tgithub.com/x/c\t[no test files]\nFAIL\tgithub.com/x/d\t1.2s\n")
	require.NoError(t, err)
	assert.Len(t, b.Sample(10), 3)
	assert.False(t, b.Hides())
}
