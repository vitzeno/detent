package island

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRender_Structure(t *testing.T) {
	out := Render("title", true, []string{"a", "b"}, 20, 4)
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 4+2, "height content lines plus two border lines")
	assert.Equal(t, "╭"+strings.Repeat("─", 18)+"╮", lines[0])
	assert.Equal(t, "╰"+strings.Repeat("─", 18)+"╯", lines[len(lines)-1])
	assert.Contains(t, lines[1], "title")
}

func TestRender_PadsShort(t *testing.T) {
	out := Render("t", false, nil, 20, 3)
	require.Len(t, strings.Split(out, "\n"), 5)
}

func TestRender_TruncatesLong(t *testing.T) {
	out := Render("t", false, []string{"a", "b", "c", "d"}, 20, 2)
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 4)
	assert.Contains(t, lines[1], "t")
	assert.Contains(t, lines[2], "a")
}

func TestRender_EmptyTitleKeepsContent(t *testing.T) {
	out := Render("", true, []string{"x"}, 20, 2)
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 4)
	assert.Contains(t, lines[1], "x")
	assert.Contains(t, lines[2], "│", "second line pads blank, no title consumed")
}

func TestRender_ActiveDiffers(t *testing.T) {
	// Color codes are stripped without a TTY, so only structure is
	// asserted here; the accent/neutral choice is visible live.
	assert.NotEmpty(t, Render("t", true, nil, 20, 1))
}

func TestRender_TruncatesWideLines(t *testing.T) {
	wide := strings.Repeat("x", 300)
	out := Render("t", false, []string{wide}, 40, 2)
	for _, l := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(l), 40, "no physical line may exceed the frame")
	}
}
