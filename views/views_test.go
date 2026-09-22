package views_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/views"
	"github.com/vitzeno/detent/viewspec"
)

// Every shipped spec has to compile against the standard vocabulary.
// A spec naming a widget nobody registered is drawable by nothing, and
// the only thing that notices is the pane going blank.
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
// trips to the same key. The ps seed drifted from this once and drew
// nothing for ps aux.
func TestShipped_CommandSpecsAreFiledUnderTheirMatch(t *testing.T) {
	for _, command := range views.Commands() {
		spec, _ := views.ForCommand(command)
		assert.Equal(t, command, spec.Match, "%s should match its own key", command)
	}
}

// ForCommand hands back a copy. Two rows drawing the same shipped spec
// must not be able to edit each other's.
func TestForCommand_HandsBackACopy(t *testing.T) {
	first, ok := views.ForCommand("ps")
	require.True(t, ok)
	first.Match = "vandalised"

	second, ok := views.ForCommand("ps")
	require.True(t, ok)
	assert.Equal(t, "ps", second.Match)
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
