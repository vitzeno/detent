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

func TestRender_TruncatesWideLines(t *testing.T) {
	wide := strings.Repeat("x", 300)
	out := Render("t", lipgloss.Color("#333333"), []string{wide}, 40, 2)
	for _, l := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(l), 40, "no physical line may exceed the frame")
	}
}
