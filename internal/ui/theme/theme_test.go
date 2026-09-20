package theme

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNames_SortedAndCoversDefault(t *testing.T) {
	names := Names()
	assert.Equal(t, []string{"dark", "dracula", "light", "solarized"}, names)
	assert.Contains(t, names, DefaultName)
}

func TestThemes_HasEveryName(t *testing.T) {
	for _, name := range Names() {
		_, ok := Themes[name]
		require.True(t, ok, "Themes missing entry for %q", name)
	}
}

func TestApply_SwitchesActiveColors(t *testing.T) {
	t.Cleanup(func() { Apply(dark) })

	Apply(Themes["light"])
	assert.Equal(t, Themes["light"].Accent, Accent)
	assert.Equal(t, Themes["light"].Border, Border)

	Apply(Themes["dracula"])
	assert.Equal(t, Themes["dracula"].Accent, Accent)
	assert.NotEqual(t, Themes["light"].Accent, Accent, "Apply must fully replace the prior theme's colors")
}

func TestDefaultName_IsAValidTheme(t *testing.T) {
	_, ok := Themes[DefaultName]
	require.True(t, ok)
}
