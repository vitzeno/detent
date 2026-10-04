package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

func TestFinder_JumpsToTheHitAndStopsFollowing(t *testing.T) {
	k, calls := finderSession(t)
	require.True(t, k.m.nav.follow)

	k.ctrl(t, 'f')
	require.Equal(t, modeFinder, k.m.mode)
	k.finderType(t, "redis tls")
	require.Equal(t, "redis tls", k.m.finder.query, "a typed space is kept")
	require.NotEmpty(t, k.m.finder.hits)
	assert.Same(t, k.m.row(calls[0]), k.m.finder.hits[0].row, "the request and its command both land on that row")

	k.press(t, "enter")
	assert.Equal(t, modeInput, k.m.mode)
	assert.Same(t, k.m.row(calls[0]), k.m.focused())
	assert.False(t, k.m.nav.follow, "a new row must not pull the cursor off what was found")
	assert.Equal(t, focusHistory, k.m.nav.focus)
}

func TestFinder_EscPutsEverythingBack(t *testing.T) {
	k, _ := finderSession(t)
	before := k.m.nav
	k.ctrl(t, 'f')
	k.finderType(t, "list")
	k.press(t, "down")
	k.press(t, "esc")

	assert.Equal(t, modeInput, k.m.mode)
	assert.Equal(t, before.cursor, k.m.nav.cursor)
	assert.Equal(t, before.follow, k.m.nav.follow)
	assert.Equal(t, focusInput, k.m.nav.focus)
	assert.True(t, k.m.prompt.Focused())
}

// Output is matched a line at a time, and a row gives one hit however
// often its output repeats the word.
func TestFinder_OutputHitsAreOneLinePerRow(t *testing.T) {
	k, calls := finderSession(t)
	k.ctrl(t, 'f')
	k.ctrl(t, 'f') // prompts
	k.ctrl(t, 'f') // commands
	k.ctrl(t, 'f') // output
	require.Equal(t, finderOutput, k.m.finder.kind)
	k.finderType(t, "6380")

	require.Len(t, k.m.finder.hits, 1)
	h := k.m.finder.hits[0]
	assert.Equal(t, "tls-port:6380", h.label)
	assert.Equal(t, []int{9, 10, 11, 12}, h.pos)

	k.press(t, "enter")
	assert.Same(t, k.m.row(calls[0]), k.m.focused())
}

// Typing must never answer a question, so one arriving while the finder is
// up waits for it to close.
func TestFinder_AQuestionWaitsUntilItCloses(t *testing.T) {
	k, _ := finderSession(t)
	k.ctrl(t, 'f')
	k.m.apply(event.ApprovalAsked{ToolCall: uuid.Must(uuid.NewV7()), Tool: "bash",
		Args: map[string]any{"command": "rm -rf build"}})
	require.Equal(t, modeFinder, k.m.mode)
	assert.Contains(t, stripANSI(k.m.finderTitle()), "a question is waiting")

	k.finderType(t, "y")
	assert.Equal(t, "y", k.m.finder.query)
	select {
	case rec := <-k.seen:
		t.Fatalf("typing in the finder published %s", rec.Event.Kind())
	case <-time.After(50 * time.Millisecond):
	}

	k.press(t, "esc")
	assert.Equal(t, modeConfirm, k.m.mode, "closing raises the question that waited")
}

// The list holds still while the agent works, as history does.
func TestFinder_NewRowsDoNotMoveTheList(t *testing.T) {
	k, _ := finderSession(t)
	k.ctrl(t, 'f')
	k.finderType(t, "go")
	k.press(t, "down")
	hits, cursor := len(k.m.finder.hits), k.m.finder.cursor

	_, evs := oneTurn("go again", "go vet ./...", "ok\n")
	for _, e := range evs {
		k.m.apply(e)
	}
	assert.Len(t, k.m.finder.hits, hits)
	assert.Equal(t, cursor, k.m.finder.cursor)
}

func TestFinder_SaysWhenAHitWasUndone(t *testing.T) {
	k, _ := finderSession(t)
	k.ctrl(t, 'f')
	k.finderType(t, "go test")
	require.NotEmpty(t, k.m.finder.hits)
	k.m.apply(event.RolledBack{Turn: k.m.finder.hits[0].block.id})

	k.press(t, "enter")
	assert.Equal(t, modeInput, k.m.mode)
	assert.Contains(t, k.m.notice.text, "no longer in history")
}

func TestFinder_SlashSearchOpensWithTheQuery(t *testing.T) {
	k, _ := finderSession(t)
	k.finderType(t, "/search tls")
	k.press(t, "enter")
	require.Equal(t, modeFinder, k.m.mode)
	assert.Equal(t, "tls", k.m.finder.query)
}

// The box floats over the panes with room round it, so the screen keeps its
// size, no line runs past the terminal, and the panes still show at the edges.
func TestFinder_DrawsInsideTheScreen(t *testing.T) {
	for _, w := range []int{120, 80} {
		k, _ := finderSession(t)
		k.m.layout.width = w
		k.m.sizeViewport()
		k.ctrl(t, 'f')
		k.finderType(t, "tls")

		screen := strings.Split(k.m.withFinder(k.m.baseView()), "\n")
		base := strings.Split(k.m.baseView(), "\n")
		assert.Len(t, screen, len(base), "width %d", w)
		for i, l := range screen {
			assert.LessOrEqual(t, lipgloss.Width(l), w, "width %d, line %d: %q", w, i, stripANSI(l))
		}
		assert.Contains(t, stripANSI(strings.Join(screen, "\n")), "> tls")

		assert.Equal(t, stripANSI(base[1]), stripANSI(screen[1]), "width %d: the panes' top shows above it", w)
		for _, l := range screen {
			if plain := stripANSI(l); strings.Contains(plain, "> tls") {
				assert.True(t, strings.HasPrefix(plain, "│ "), "width %d: the output pane shows left of it: %q", w, plain)
			}
		}
	}
}

func TestHighlight_FitsItsWidthAndKeepsTheMatchInView(t *testing.T) {
	label := strings.Repeat("x", 60) + "needle"
	for _, w := range []int{10, 20, 80} {
		out := stripANSI(highlight(label, []int{60, 61, 62, 63, 64, 65}, w))
		assert.LessOrEqual(t, lipgloss.Width(out), w, "width %d", w)
		if w >= 12 {
			assert.Contains(t, out, "needle", "width %d", w)
		}
	}
}

// A session of three requests, each one command, so a hit names its row.
func finderSession(t *testing.T) (*keyed, []uuid.UUID) {
	t.Helper()
	k := newKeyed(t)
	var calls []uuid.UUID
	for i, c := range []struct{ prompt, command, out string }{
		{"why does redis refuse tls", "redis-cli --tls info", "# Server\nredis_version:7.2.4\ntls-port:6380\n"},
		{"list the go files", "find . -name '*.go'", "main.go\nui/model.go\n"},
		{"run the tests", "go test ./...", "ok  ui\nFAIL engine\n"},
	} {
		turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		calls = append(calls, call)
		for _, e := range []event.Event{
			event.TurnStarted{Turn: turn, N: i + 1, Prompt: c.prompt},
			event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": c.command}},
			event.ToolCallEnded{ToolCall: call, Result: event.Result{Stdout: c.out}},
			event.TurnEnded{Turn: turn, Reason: event.EndDone},
		} {
			k.m.apply(e)
		}
	}
	k.m.sizeViewport()
	return k, calls
}

// finderType types text a rune at a time.
func (k *keyed) finderType(t *testing.T, text string) {
	t.Helper()
	for _, r := range text {
		k.send(t, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (k *keyed) ctrl(t *testing.T, r rune) {
	t.Helper()
	k.send(t, tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl})
}

func (k *keyed) send(t *testing.T, msg tea.KeyPressMsg) {
	t.Helper()
	var cmd tea.Cmd
	k.m, cmd = k.m.update(msg)
	runCmd(cmd)
}
