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

// Tool names are told apart by colour alone, so each theme's must be readable
// and far enough from each other to read as different hues.
func TestThemes_ToolColoursAreReadableAndDistinct(t *testing.T) {
	for _, name := range Names() {
		th := Themes[name]
		t.Run(name, func(t *testing.T) {
			tools := map[string]color.Color{"Shell": th.Tools.Shell, "Read": th.Tools.Read,
				"Write": th.Tools.Write, "Web": th.Tools.Web, "Server": th.Tools.Server, "Agent": th.Tools.Agent,
				"Other": th.Tools.Other}
			bg := lum(t, th.Background)
			for what, c := range tools {
				require.NotNil(t, c, what)
				assert.GreaterOrEqual(t, contrast(lum(t, c), bg), 3.0, "%s is too faint to read", what)
				for other, d := range tools {
					if what < other {
						assert.GreaterOrEqual(t, distance(c, d), 90.0, "%s and %s look alike", what, other)
					}
				}
			}
		})
	}
}

// distance is how far apart two colours are in RGB, 0 to about 441.
func distance(a, b color.Color) float64 {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	d := func(x, y uint32) float64 { return float64(int(x>>8) - int(y>>8)) }
	return math.Sqrt(d(ar, br)*d(ar, br) + d(ag, bg)*d(ag, bg) + d(ab, bb)*d(ab, bb))
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
				{"TextFaint", th.TextFaint, 1.8}, // below WCAG on purpose: faint is for what may be skipped
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

// A command awaiting approval is drawn in TextPrimary on Border, and must read there.
func TestThemes_CommandReadableOnItsPanel(t *testing.T) {
	for _, name := range Names() {
		th := Themes[name]
		got := contrast(lum(t, th.TextPrimary), lum(t, th.Border))
		assert.GreaterOrEqual(t, got, 4.5, "%s: TextPrimary on Border is %.2f", name, got)
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
