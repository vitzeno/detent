package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vitzeno/detent/ui/theme"
)

// Locks the risk in theme switching: package styles are baked at init
// time, so theme.Apply alone doesn't touch them.
func TestRefreshStyles_PicksUpThemeChange(t *testing.T) {
	t.Cleanup(func() {
		theme.Apply(theme.Themes[theme.DefaultName])
		RefreshStyles()
	})

	theme.Apply(theme.Themes["dracula"])
	RefreshStyles()
	dracula := styleBrand.Render("x")

	theme.Apply(theme.Themes["light"])
	RefreshStyles()
	light := styleBrand.Render("x")

	assert.NotEqual(t, dracula, light, "RefreshStyles must rebuild styleBrand from the new theme")
}
