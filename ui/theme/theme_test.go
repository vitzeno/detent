package theme

import (
	"image/color"
	"math"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNames_SortedAndCoversDefault(t *testing.T) {
	names := Names()
	assert.Equal(t, []string{"dark", "dracula", "light", "solarized"}, names)
	assert.Contains(t, names, DefaultName)
}

func TestApply_SwitchesTheActiveTheme(t *testing.T) {
	was := Current()
	t.Cleanup(func() { Apply(was) })

	assert.Equal(t, Themes[DefaultName], was, "the default is active before any Apply")
	for _, name := range Names() {
		Apply(Themes[name])
		assert.Equal(t, Themes[name], Current(), "theme %s", name)
	}
}

// A field one theme forgets draws in the terminal's own colour.
func TestThemes_SetEveryField(t *testing.T) {
	for _, name := range Names() {
		v := reflect.ValueOf(Themes[name])
		for i := range v.NumField() {
			assert.False(t, v.Field(i).IsZero(), "%s leaves %s unset", name, v.Type().Field(i).Name)
		}
	}
}

// Every theme has to be readable against its own background.
func TestThemes_ReadableAgainstTheirBackground(t *testing.T) {
	for _, name := range Names() {
		th := Themes[name]
		t.Run(name, func(t *testing.T) {
			require.NotNil(t, th.Background, "every theme declares the ground it was picked for")
			bg := lum(t, th.Background)

			for _, c := range []struct {
				what string
				col  color.Color
				min  float64
			}{
				{"TextPrimary", th.TextPrimary, 4.5},
				{"TextMuted", th.TextMuted, 3.0},
				{"TextFaint", th.TextFaint, 1.8},
				{"Accent", th.Accent, 3.0},
				{"Safe", th.Safe, 3.0},
				{"Caution", th.Caution, 3.0},
				{"Danger", th.Danger, 3.0},
			} {
				require.NotNil(t, c.col, c.what)
				got := contrast(lum(t, c.col), bg)
				assert.GreaterOrEqual(t, got, c.min,
					"%s contrast %.2f against the background is too low to read", c.what, got)
			}
		})
	}
}

// lum is relative luminance per WCAG, from a theme colour.
func lum(t *testing.T, c color.Color) float64 {
	t.Helper()
	r, g, b, _ := c.RGBA()
	f := func(v uint32) float64 {
		s := float64(v) / 65535
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*f(r) + 0.7152*f(g) + 0.0722*f(b)
}

func contrast(a, b float64) float64 {
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}
