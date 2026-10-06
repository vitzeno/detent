package ui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// home and g reach the first row and end and G the newest, which follows again.
func TestTopAndEnd_History(t *testing.T) {
	for _, keys := range [][2]string{{"home", "end"}, {"g", "G"}} {
		k, _ := finderSession(t)
		k.press(t, "tab")
		require.Equal(t, ownerHistory, k.m.owner())
		k.press(t, keys[0])
		assert.Zero(t, k.m.nav.cursor, keys[0])
		assert.False(t, k.m.nav.follow)
		k.press(t, keys[1])
		assert.Equal(t, len(k.m.rows())-1, k.m.nav.cursor, keys[1])
		assert.True(t, k.m.nav.follow, "the newest row is followed again")
	}
}

func TestTopAndEnd_Output(t *testing.T) {
	k := longOutput(t)
	k.press(t, "tab")
	k.press(t, "tab")
	require.Equal(t, ownerOutput, k.m.owner())
	k.press(t, "G")
	assert.True(t, k.m.output.AtBottom())
	k.press(t, "g")
	assert.Zero(t, k.m.output.YOffset())
	k.press(t, "end")
	assert.True(t, k.m.output.AtBottom())
}

// The prompt types g and G, and leaves home and end to the text.
func TestTopAndEnd_ThePromptTypesTheLetters(t *testing.T) {
	k := longOutput(t)
	k.press(t, "g")
	k.press(t, "G")
	assert.Equal(t, "gG", k.m.prompt.Value())
}

// end on a tall approval shows its last line, which is what lets y run it.
func TestTopAndEnd_AnApprovalReadToItsEnd(t *testing.T) {
	var script strings.Builder
	for i := range 60 {
		fmt.Fprintf(&script, "echo step %d\n", i)
	}
	k := queued(t, script.String()+"rm -rf ~/important")
	require.False(t, k.m.confirmReady())
	k.press(t, "end")
	assert.True(t, k.m.confirmReady())
	k.press(t, "home")
	assert.Zero(t, k.m.confirm.top)
}

// The finder's letters are its query, so only home and end jump.
func TestTopAndEnd_Finder(t *testing.T) {
	k, _ := finderSession(t)
	k.m, _ = k.m.openFinder("")
	k.finderType(t, "e")
	require.Greater(t, len(k.finder().hits), 1)
	k.press(t, "end")
	assert.Equal(t, len(k.finder().hits)-1, k.finder().cursor)
	k.press(t, "home")
	assert.Zero(t, k.finder().cursor)
	k.press(t, "G")
	assert.Equal(t, "eG", k.finder().query)
}

func TestTopAndEnd_Inspector(t *testing.T) {
	k := inspecting(t)
	for range 3 {
		k.m.apply(event.ToolCallProposed{ToolCall: uuid.Must(uuid.NewV7()), Tool: "bash",
			Args: map[string]any{"command": "ls"}, Agent: k.insp().agent.id})
	}
	rows := k.m.inspectorRows(k.insp().agent)
	require.Greater(t, len(rows), 1)
	k.press(t, "G")
	assert.Equal(t, len(rows)-1, k.insp().cursor)
	k.press(t, "g")
	assert.Zero(t, k.insp().cursor)
}

// In the list g and G pick the first and last session, and in the preview they
// scroll to its oldest line and back to its newest.
func TestTopAndEnd_Resume(t *testing.T) {
	k, other := resumable(t)
	k.openResume()
	k.press(t, "G")
	assert.Equal(t, len(k.m.sessions)-1, k.picker().cursor)
	k.press(t, "g")
	assert.Zero(t, k.picker().cursor)

	k.press(t, "G")
	long := storedSession(other, "the sandbox bug")
	for i := range 60 {
		turn := uuid.Must(uuid.NewV7())
		long.Records = append(long.Records, asRecords([]event.Event{
			event.TurnStarted{Turn: turn, N: i + 2, Prompt: fmt.Sprint("request ", i)},
			event.TurnEnded{Turn: turn, Reason: event.EndDone},
		})...)
	}
	k.m.apply(long)
	k.press(t, "tab")
	k.press(t, "g")
	_, right := k.m.resumePaneWidths()
	top := len(k.picker().history(k.m, right)) - k.m.modalPaneHeight()
	require.Positive(t, top, "the test needs a preview taller than its pane")
	assert.Equal(t, top, k.picker().scroll, "held at the oldest line, not past it")
	k.press(t, "down")
	assert.Equal(t, top-1, k.picker().scroll, "so the next key moves at once")
	k.press(t, "G")
	assert.Zero(t, k.picker().scroll)
}

// In the diff g and G reach its first and last line, and in the list the files.
func TestTopAndEnd_Review(t *testing.T) {
	k := loadedReview(t)
	require.True(t, k.m.review.diffFocused)
	k.press(t, "G")
	assert.Equal(t, len(k.m.reviewRows())-1, k.m.review.line)
	k.press(t, "g")
	assert.Zero(t, k.m.review.line)
	k.press(t, "tab")
	k.press(t, "end")
	assert.Equal(t, len(k.m.review.files)-1, k.m.review.file)
	k.press(t, "home")
	assert.Zero(t, k.m.review.file)
}

// longOutput is a finished command whose output is taller than its pane.
func longOutput(t *testing.T) *keyed {
	t.Helper()
	k := newKeyed(t)
	turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, ev := range []event.Event{
		event.TurnStarted{Turn: turn, N: 1, Prompt: "p"},
		event.ToolCallProposed{ToolCall: call, Tool: event.ToolBash, Args: map[string]any{"command": "seq 200"}},
		event.ToolCallEnded{ToolCall: call, Result: event.Result{Stdout: strings.Repeat("line\n", 200)}},
		event.TurnEnded{Turn: turn, Reason: event.EndDone},
	} {
		k.m.apply(ev)
	}
	k.m.sizeViewport()
	require.Greater(t, k.m.output.TotalLineCount(), k.m.output.Height(), "the test needs output to scroll")
	return k
}

// /help is drawn from keyGroups, so a binding added to keymap but not there
// would work and be listed nowhere.
func TestHelp_ListsEveryBinding(t *testing.T) {
	listed := 0
	for _, g := range keyGroups() {
		for _, b := range g.keys {
			require.NotEmpty(t, b.Keys(), "%s lists a binding with no keys", g.place)
			require.NotEmpty(t, b.Help().Desc, "%s lists %v saying nothing", g.place, b.Keys())
			listed++
		}
	}
	assert.Equal(t, bindingsIn(reflect.ValueOf(keymap)), listed)

	page := ansi.Strip(strings.Join(newKeyed(t).m.helpLines(), "\n"))
	assert.Contains(t, page, "home g")
	assert.Contains(t, page, "the comment after, across files")
}

// bindingsIn counts the bindings with keys in v, a keymap or a part of one.
func bindingsIn(v reflect.Value) int {
	if v.Type() == reflect.TypeFor[key.Binding]() {
		if v.IsZero() {
			return 0
		}
		return 1
	}
	n := 0
	for _, f := range v.Fields() {
		n += bindingsIn(f)
	}
	return n
}
