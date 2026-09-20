package slash

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchSlash(t *testing.T) {
	assert.Len(t, Match("/"), 6)
	assert.Equal(t, []Cmd{{"/quit", "quit detent"}}, Match("/q"))
	assert.Equal(t, []Cmd{{"/abort", "abort the running command"}}, Match("/a"))
	assert.Empty(t, Match("/x"))
	assert.Empty(t, Match("quit"), "no leading slash matches nothing")
	assert.True(t, Exact("/quit"))
	assert.False(t, Exact("/q"))
}

func TestView(t *testing.T) {
	v := View(Match("/"), 0)
	require.Contains(t, v, "/quit")
	require.Contains(t, v, "/abort")
	require.Contains(t, v, "/tree")
	require.Contains(t, v, "/usage")
	require.Contains(t, v, "/rollback")
	require.Contains(t, v, "/help")
	assert.Empty(t, View(nil, 0))
}

// TestView_AlignsUnderColor guards against padding a name after it's
// already been ANSI-styled: fmt's width verbs count escape bytes too,
// so %-10s applied post-Render silently drops the padding. lipgloss v2
// styles off-TTY as well, so the escapes are here without forcing a
// color profile — which is why this went unnoticed under v1.
func TestView_AlignsUnderColor(t *testing.T) {
	v := View(Match("/"), 0)
	require.Contains(t, v, "\x1b[", "lipgloss must emit styling for this test to mean anything")
	ansi := regexp.MustCompile("\x1b\\[[0-9;]*m")
	lines := ansi.ReplaceAllString(v, "")
	assert.Contains(t, lines, "▸ /quit      quit detent")
	assert.Contains(t, lines, "  /abort     abort the running command")
}
