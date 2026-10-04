package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

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
	k.m.apply(event.ApprovalAsked{ToolCall: call, Tool: "bash"})
	require.Equal(t, modeConfirm, k.m.mode)

	k.press(t, "y")
	got, ok := k.intent(t).(event.ResolveApproval)
	require.True(t, ok)
	assert.Equal(t, call, got.ToolCall)
	assert.True(t, got.Approved)
	assert.Equal(t, modeInput, k.m.mode)
}

// A question arriving mid-sentence waits for the bar, so the enter that sends
// the steering message cannot approve `rm -rf`.
func TestKeys_AnApprovalWaitsBehindWhatWasTyped(t *testing.T) {
	k := newKeyed(t)
	call := uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "clean up"})
	k.finderType(t, "use")
	k.m.apply(event.ApprovalAsked{ToolCall: call, Tool: "bash",
		Args: map[string]any{"command": "rm -rf ~/work"}})
	require.Equal(t, modeInput, k.m.mode, "held while the bar holds text")
	assert.Contains(t, k.m.statusHint(), "a question is waiting")

	k.press(t, "enter")
	got, ok := k.intent(t).(event.SubmitPrompt)
	require.True(t, ok, "enter sent what was typed, and answered nothing")
	assert.Equal(t, "use", got.Text)
	assert.Equal(t, modeConfirm, k.m.mode, "the bar is empty, so the question goes up")
}

// The first key of the next message must not answer a question that has
// only just appeared.
func TestKeys_AKeyTooSoonAfterAQuestionIsNotAnAnswer(t *testing.T) {
	was := questionSettle
	questionSettle = time.Hour
	defer func() { questionSettle = was }()

	for _, tt := range []struct {
		name string
		ask  event.Event
		key  string
	}{
		{"approval", event.ApprovalAsked{ToolCall: uuid.Must(uuid.NewV7()), Tool: "bash"}, "y"},
		{"step bound", event.BoundReached{Turn: uuid.Must(uuid.NewV7())}, "enter"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			k := newKeyed(t)
			k.m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "go"})
			k.m.apply(tt.ask)
			asked := k.m.mode
			k.press(t, tt.key)
			assert.Equal(t, asked, k.m.mode, "still asking")
			select {
			case rec := <-k.seen:
				t.Fatalf("a key too soon published %s", rec.Event.Kind())
			case <-time.After(50 * time.Millisecond):
			}

			k.m.askedAt = time.Now().Add(-2 * time.Hour)
			k.press(t, tt.key)
			assert.Equal(t, modeInput, k.m.mode, "once settled the same key answers")
		})
	}
}

// A command taller than the screen must not be approvable from its
// head: the tail is where the damage would be.
func TestKeys_TallApprovalRunsOnlyOnceReadToTheEnd(t *testing.T) {
	k := newKeyed(t)
	call := uuid.Must(uuid.NewV7())
	var script strings.Builder
	script.WriteString("cat > setup.sh <<'EOF'\n")
	for i := range 60 {
		fmt.Fprintf(&script, "echo step %d\n", i)
	}
	script.WriteString("EOF\nrm -rf ~/important")
	k.m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "set up"})
	k.m.apply(event.ApprovalAsked{ToolCall: call, Tool: "bash",
		Args: map[string]any{"command": script.String()}, Rationale: "writes a file"})
	k.m.sizeViewport()

	screen := func() string { return ansi.Strip(k.m.baseView()) }
	assert.LessOrEqual(t, strings.Count(screen(), "\n")+1, k.m.layout.height, "the frame fits the screen")
	assert.NotContains(t, screen(), "rm -rf ~/important", "the tail is off screen")
	assert.NotContains(t, screen(), "[y/enter] run", "so running it is not offered")
	assert.Contains(t, screen(), "more below")

	k.press(t, "y")
	assert.Equal(t, modeConfirm, k.m.mode, "y is refused until the end is read")
	require.NotNil(t, k.m.asking)

	for range 20 {
		k.press(t, "pgdown")
	}
	assert.LessOrEqual(t, strings.Count(screen(), "\n")+1, k.m.layout.height)
	assert.Contains(t, screen(), "rm -rf ~/important")
	assert.Contains(t, screen(), "[y/enter] run")

	k.press(t, "y")
	got, ok := k.intent(t).(event.ResolveApproval)
	require.True(t, ok)
	assert.Equal(t, call, got.ToolCall)
	assert.True(t, got.Approved)
}

// What the model proposes is drawn, never obeyed: an escape in it must
// not reach the terminal on the rows a human reads to approve it.
func TestKeys_ProposedTextCannotDriveTheTerminal(t *testing.T) {
	k := newKeyed(t)
	call := uuid.Must(uuid.NewV7())
	const sneaky = "ls\x1b]52;c;cm0gLXJmIH4=\x07\x1b[2K\rrm -rf ~"
	k.m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "look"})
	k.m.apply(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": sneaky}})
	k.m.apply(event.ApprovalAsked{ToolCall: call, Tool: "bash", Args: map[string]any{"command": sneaky}})
	k.m.sizeViewport()

	view := k.m.baseView()
	assert.NotContains(t, view, "\x1b]52", "no OSC from the command")
	assert.NotContains(t, view, "\x1b[2K")
	assert.NotContains(t, view, "\r")
	assert.Contains(t, ansi.Strip(view), "^[]52;c;", "it is shown instead")
}

// The engine aborts a running Turn on /new and starts a new session, and
// history starts again when that session does.
func TestKeys_NewResetsWhetherOrNotATurnRuns(t *testing.T) {
	for _, running := range []bool{true, false} {
		k := newKeyed(t)
		turn := uuid.Must(uuid.NewV7())
		k.m.apply(event.SessionStarted{Session: uuid.Must(uuid.NewV7())})
		k.m.apply(event.TurnStarted{Turn: turn, N: 1, Prompt: "slow"})
		if !running {
			k.m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone})
		}
		for _, r := range "/new" {
			k.press(t, string(r))
		}
		k.press(t, "enter")
		_, ok := k.intent(t).(event.ResetSession)
		require.True(t, ok, "running=%v resets", running)
		k.m.apply(event.SessionStarted{Session: uuid.Must(uuid.NewV7())})
		assert.Empty(t, k.m.blocks)
	}
}

func TestUndo_IsRefusedWhileATurnRuns(t *testing.T) {
	_, evs := aTurn("slow")
	m := feed(t, evs...)
	got, cmd := m.runUndo("/undo")
	assert.Nil(t, cmd)
	assert.Equal(t, modeInput, got.mode, "no page offering what the engine will refuse")
	assert.Contains(t, got.notice.text, "abort")
}

// Every key but y cancels a delete, so the hint must not offer tab.
func TestStatusHint_SaysWhatCancelsADelete(t *testing.T) {
	m := feed(t)
	m.mode = modeForget
	assert.Equal(t, "[y] delete · [↑/↓] read · any other key cancels", m.statusHint())
}

// A Turn whose block is already gone still ends: nothing may be left
// spinning on it.
func TestApply_EndingAnUnknownTurnStillSettles(t *testing.T) {
	_, evs := aTurn("going")
	m := feed(t, evs...)
	m.blocks = nil
	m.apply(event.TurnEnded{Turn: uuid.Must(uuid.NewV7()), Reason: event.EndAborted})
	assert.Nil(t, m.cur)
	assert.False(t, m.waiting)
	assert.False(t, m.spinning())
	assert.Equal(t, ownerInput, m.owner())
}

func TestKeys_BoundIsAnswered(t *testing.T) {
	k := newKeyed(t)
	turn := uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, N: 1, Prompt: "big"})
	k.m.apply(event.BoundReached{Turn: turn, Steps: 50})
	require.Equal(t, modeBound, k.m.mode, "the fact alone puts the question up")

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

// The binding is only worth having if it actually arrives, so this
// asserts the string handleKey switches on rather than trusting it.
func TestKeys_ShiftTabIsSpeltTheWayTheTerminalSendsIt(t *testing.T) {
	msg := tea.KeyPressMsg{Code: keyCode("shift+tab"), Mod: keyMod("shift+tab")}
	assert.Equal(t, "shift+tab", msg.String())
}

// keyed is a Model on a real bus, collecting the intents it publishes:
// publishing is the only way a key reaches the engine.
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
	m := New(t.Context(), bus, SessionInfo{})
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	return &keyed{m: m, bus: bus, seen: seen}
}

// press sends a key and runs whatever command came back, which is
// where the publish happens.
func (k *keyed) press(t *testing.T, key string) {
	t.Helper()
	var cmd tea.Cmd
	k.m, cmd = k.m.update(tea.KeyPressMsg{
		Code: keyCode(key), Text: keyText(key), Mod: keyMod(key)})
	runCmd(cmd)
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

// intentOf is the next intent of kind, skipping any other.
func (k *keyed) intentOf(t *testing.T, kind event.Kind) event.Event {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case rec := <-k.seen:
			if rec.Event.Kind() == kind {
				return rec.Event
			}
		case <-deadline:
			t.Fatalf("no %s was published", kind)
			return nil
		}
	}
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
	case "tab", "shift+tab":
		return tea.KeyTab
	case "down":
		return tea.KeyDown
	case "pgdown":
		return tea.KeyPgDown
	}
	return rune(key[0])
}

// shift+tab arrives as tab carrying the modifier, which is how the
// terminal sends backtab and what handleKey matches on.
func keyMod(key string) tea.KeyMod {
	if key == "shift+tab" {
		return tea.ModShift
	}
	return 0
}

func keyText(key string) string {
	if len(key) == 1 {
		return key
	}
	return ""
}
