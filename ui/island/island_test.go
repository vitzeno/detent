package island

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// plain drops the ANSI styling lipgloss v2 emits even off-TTY.
func plain(s string) string {
	return ansi.Strip(s)
}

func TestRender_Structure(t *testing.T) {
	out := Render("title", lipgloss.Color("#ff0000"), []string{"a", "b"}, 20, 4)
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 4+2, "height content lines plus two border lines")
	assert.Equal(t, "╭"+strings.Repeat("─", 18)+"╮", plain(lines[0]),
		"the island renders exactly the width it was asked for")
	assert.Equal(t, "╰"+strings.Repeat("─", 18)+"╯", plain(lines[len(lines)-1]))
	assert.Contains(t, lines[1], "title")
}

func TestRender_PadsShort(t *testing.T) {
	out := Render("t", lipgloss.Color("#333333"), nil, 20, 3)
	require.Len(t, strings.Split(out, "\n"), 5)
}

func TestRender_TruncatesLong(t *testing.T) {
	out := Render("t", lipgloss.Color("#333333"), []string{"a", "b", "c", "d"}, 20, 2)
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 4)
	assert.Contains(t, lines[1], "t")
	assert.Contains(t, lines[2], "a")
}

func TestRender_EmptyTitleKeepsContent(t *testing.T) {
	out := Render("", lipgloss.Color("#ff0000"), []string{"x"}, 20, 2)
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 4)
	assert.Contains(t, lines[1], "x")
	assert.Contains(t, lines[2], "│", "second line pads blank, no title consumed")
}

// Command output reaches here raw, so tabs and embedded newlines must
// not let lipgloss grow the frame after it was measured.
func TestRender_HoldsItsSize(t *testing.T) {
	tests := []struct {
		name          string
		lines         []string
		width, height int
	}{
		{"a wide line", []string{strings.Repeat("x", 300)}, 40, 2},
		{"tabs", []string{"a\tb\tc\td\te\tf"}, 14, 2},
		{"embedded newlines", []string{"a\nb\nc"}, 20, 2},
		{"a carriage return", []string{"50%\r100%"}, 20, 2},
		{"wide runes", []string{"日本語日本語日本語日本語"}, 12, 2},
		{"zero height", []string{"a"}, 20, 0},
		{"negative height", []string{"a"}, 20, -3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := Render("t", lipgloss.Color("#333333"), tt.lines, tt.width, tt.height)
			lines := strings.Split(out, "\n")
			assert.Len(t, lines, max(1, tt.height)+2)
			for _, l := range lines {
				assert.Equal(t, tt.width, lipgloss.Width(l), "no physical line may leave the frame")
			}
		})
	}
}

func TestRender_SplitsALineHoldingNewlines(t *testing.T) {
	out := Render("", lipgloss.Color("#333333"), []string{"a\nb"}, 20, 3)
	lines := strings.Split(plain(out), "\n")
	assert.Contains(t, lines[1], "a")
	assert.Contains(t, lines[2], "b")
}
