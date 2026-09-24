package ui

import (
	"context"
	"github.com/google/uuid"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// keyed builds a Model wired to a real bus, and collects the intents
// it publishes. Every key that changes what the engine is doing must
// show up here, because publishing is the only way ui reaches it.
type keyed struct {
	m    Model
	bus  *event.Bus
	seen <-chan event.Record
}

func newKeyed(t *testing.T) *keyed {
	t.Helper()
	bus := event.New()
	seen, stop := bus.Subscribe(event.Intents())
	t.Cleanup(stop)
	m := New(context.Background(), bus, SessionInfo{})
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	return &keyed{m: m, bus: bus, seen: seen}
}

// press sends a key and runs whatever command came back, which is
// where the publish happens. tea.Batch returns its children rather
// than running them, so this does what the runtime would.
func (k *keyed) press(t *testing.T, key string) {
	t.Helper()
	next, cmd := k.m.Update(tea.KeyPressMsg{Code: keyCode(key), Text: keyText(key)})
	k.m = next.(Model)
	runCmd(cmd)
}

// runCmd runs a command the way the runtime does: concurrently, so a
// spinner tick's delay does not hold up the publish beside it.
func runCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				runCmd(c)
			}
		}
	case <-time.After(200 * time.Millisecond):
	}
}

func keyCode(key string) rune {
	switch key {
	case "enter":
		return tea.KeyEnter
	case "esc":
		return tea.KeyEscape
	}
	return rune(key[0])
}

func keyText(key string) string {
	if len(key) == 1 {
		return key
	}
	return ""
}

func (k *keyed) intent(t *testing.T) event.Event {
	t.Helper()
	select {
	case rec := <-k.seen:
		return rec.Event
	case <-time.After(2 * time.Second):
		t.Fatal("no intent was published")
		return nil
	}
}

// Typing and pressing enter must publish a prompt. This is the whole
// path from keyboard to engine, and nothing else connects them.
func TestKeys_EnterSubmitsThePrompt(t *testing.T) {
	k := newKeyed(t)
	for _, r := range "list the files" {
		k.press(t, string(r))
	}
	require.Equal(t, "list the files", k.m.prompt.Value(), "the keys reached the input")

	k.press(t, "enter")
	got, ok := k.intent(t).(event.SubmitPrompt)
	require.True(t, ok, "enter must publish SubmitPrompt")
	assert.Equal(t, "list the files", got.Text)
	assert.Empty(t, k.m.prompt.Value(), "and clear the box")
}

func TestKeys_ApprovalIsAnswered(t *testing.T) {
	k := newKeyed(t)
	call := uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "clean"})
	k.m.apply(event.ApprovalAsked{Call: call, Tool: "bash"})
	require.Equal(t, modeConfirm, k.m.mode)

	k.press(t, "y")
	got, ok := k.intent(t).(event.ResolveApproval)
	require.True(t, ok)
	assert.Equal(t, call, got.Call)
	assert.True(t, got.Approved)
	assert.Equal(t, modeInput, k.m.mode)
}

func TestKeys_BoundIsAnswered(t *testing.T) {
	k := newKeyed(t)
	turn := uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, N: 1, Prompt: "big"})
	k.m.apply(event.BoundReached{Turn: turn, Steps: 50})
	k.m.mode = modeBound

	k.press(t, "n")
	got, ok := k.intent(t).(event.Continue)
	require.True(t, ok)
	assert.False(t, got.Approved)
}

// esc aborts a running request rather than only moving focus.
func TestKeys_EscapeAbortsARunningRequest(t *testing.T) {
	k := newKeyed(t)
	turn := uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, N: 1, Prompt: "slow"})

	k.press(t, "esc")
	got, ok := k.intent(t).(event.Abort)
	require.True(t, ok, "esc must abort while something runs")
	assert.Equal(t, turn, got.Turn)
}

func TestKeys_SlashCommandsDoNotReachTheEngine(t *testing.T) {
	k := newKeyed(t)
	for _, r := range "/help" {
		k.press(t, string(r))
	}
	k.press(t, "enter")
	assert.Equal(t, panelHelp, k.m.panel.open, "a slash command is ui's own")

	select {
	case rec := <-k.seen:
		t.Fatalf("/help published %s; slash commands are local", rec.Event.Kind())
	case <-time.After(100 * time.Millisecond):
	}
}

// Typing while a request runs steers it: the engine turns a prompt
// sent mid-Turn into NoteContext rather than starting a new one.
func TestBusy_TypingReachesThePromptAndEnterSteers(t *testing.T) {
	k := newKeyed(t)
	k.m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "go"})
	require.True(t, k.m.waiting, "the test needs the busy path")
	require.Equal(t, ownerBusy, k.m.owner())

	for _, key := range []string{"u", "s", "e", " ", "g", "o"} {
		k.press(t, key)
	}
	require.Equal(t, "use go", k.m.prompt.Value(), "keys did not reach the prompt while busy")

	k.press(t, "enter")
	sent, ok := k.intent(t).(event.SubmitPrompt)
	require.True(t, ok, "enter while busy published no prompt")
	assert.Equal(t, "use go", sent.Text)
}

// Slash commands must keep working on the busy path, which is what it
// narrowed the keys for in the first place.
func TestBusy_SlashStillOpens(t *testing.T) {
	k := newKeyed(t)
	k.m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "go"})

	k.press(t, "/")
	assert.True(t, k.m.prompt.Open(), "the slash dropdown did not open while busy")
}

// A note is queued with no fact of its own, so without this the text
// vanishes and the human cannot tell it was taken.
func TestBusy_SteeringSaysSo(t *testing.T) {
	k := newKeyed(t)
	k.m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "go"})
	for _, key := range []string{"u", "s", "e", " ", "g", "o"} {
		k.press(t, key)
	}
	k.press(t, "enter")

	assert.Contains(t, k.m.notice.text, "steering")
	assert.False(t, k.m.notice.bad)
	assert.Empty(t, k.m.prompt.Value(), "the prompt should clear either way")
}

// endTurn clears cur, so a prompt after one finishes is a new request
// rather than steering the Turn that just ended.
func TestSubmit_AfterATurnEndsIsANewRequestNotSteering(t *testing.T) {
	k := newKeyed(t)
	turn := uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, N: 1, Prompt: "go"})
	k.m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone})

	for _, key := range []string{"n", "e", "x", "t"} {
		k.press(t, key)
	}
	k.press(t, "enter")

	assert.Empty(t, k.m.notice.text, "a new request was called steering")
	assert.True(t, k.m.waiting, "a new request has to show as waiting")
}
