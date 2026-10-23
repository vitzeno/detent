package markdown

import (
	"testing"

	"charm.land/glamour/v2/styles"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/ui/theme"
)

func TestRender(t *testing.T) {
	out, err := Render("# Hi\n\nbody\n", "dark", 60)
	require.NoError(t, err)
	assert.Contains(t, out, "Hi")
}

// A typo in a theme's style name would drop every document to plain
// text with no error anyone sees.
func TestRender_EveryThemeNamesAStyleGlamourKnows(t *testing.T) {
	for _, name := range theme.Names() {
		style := theme.Themes[name].Markdown
		assert.Contains(t, styles.DefaultStyles, style, "theme %s", name)
		_, err := Render("# x", style, 40)
		assert.NoError(t, err, "theme %s", name)
	}
}
