package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"

	"github.com/vitzeno/detent/internal/ui/theme"
)

// Locks the risk in theme switching: package styles are baked at init
// time, so theme.Apply alone doesn't touch them.
func TestRefreshStyles_PicksUpThemeChange(t *testing.T) {
	// go test pipes stdout, so lipgloss defaults to no-color.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prev)
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
