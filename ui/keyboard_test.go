package ui

import (
	"context"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// TestKitty_ShiftEnterReachesTheNewlineBinding feeds the real escape
// sequence a Kitty-protocol terminal sends for shift+enter, rather than
// a synthetic KeyPressMsg, so the parser is under test too — that chain
// is the whole reason for the v2 migration and nothing else covers it.
func TestKitty_ShiftEnterReachesTheNewlineBinding(t *testing.T) {
	const (
		shiftEnter = "\x1b[13;2u"
		ctrlC      = "\x03"
	)
	in := strings.NewReader("a" + shiftEnter + "b" + ctrlC)

	m := New(context.Background(), newFakeDriver(), SessionInfo{Proposer: "test-model"})
	p := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(io.Discard))

	final, err := p.Run()
	require.NoError(t, err)

	fm, ok := final.(Model)
	require.True(t, ok)
	require.Equal(t, "a\nb", fm.prompt.Value(),
		"shift+enter must break the line without submitting the goal")
	require.Empty(t, fm.blocks, "and must not start a goal")
}

// TestKitty_HintFollowsTheTerminalsAnswer feeds the reply a terminal
// sends to the CSI?u support query. A zero flags answer means the
// terminal declined, and the hint has to name a key that still works
// there rather than one it can't tell apart from enter.
func TestKitty_HintFollowsTheTerminalsAnswer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply string
		want  string
	}{
		{"no answer at all", "", "[alt+enter] newline"},
		{"disambiguation only", "\x1b[?1u", "[shift+enter] newline"},
		{"every enhancement", "\x1b[?15u", "[shift+enter] newline"},
		{"declined", "\x1b[?0u", "[alt+enter] newline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(context.Background(), newFakeDriver(), SessionInfo{Proposer: "test-model"})
			p := tea.NewProgram(m,
				tea.WithInput(strings.NewReader(tc.reply+"\x03")),
				tea.WithOutput(io.Discard))

			final, err := p.Run()
			require.NoError(t, err)
			require.Contains(t, final.(Model).statusHint(), tc.want)
		})
	}
}
