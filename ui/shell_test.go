package ui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/theme"
)

// The input bar in shell mode, and what the two facts draw.

func TestShell_EnterRunsACommandRatherThanAskingForOne(t *testing.T) {
	k := newKeyed(t)
	k.press(t, "shift+tab")
	require.True(t, k.m.prompt.shell)

	for _, r := range "ls" {
		k.press(t, string(r))
	}
	k.press(t, "enter")

	got, ok := k.intent(t).(event.RunCommand)
	require.True(t, ok, "enter in shell mode must publish RunCommand, not SubmitPrompt")
	assert.Equal(t, "ls", got.Text)
	assert.Empty(t, k.m.prompt.Value(), "and clear the box")
}

// /usr/bin has to be typable, so the dropdown cannot own the key.
func TestShell_SlashIsAPathNotADropdown(t *testing.T) {
	k := newKeyed(t)
	k.press(t, "/")
	require.True(t, k.m.prompt.Open(), "a request still gets the dropdown")

	k.press(t, "shift+tab")
	assert.False(t, k.m.prompt.Open(), "switching closes one that was open")

	// The slash already typed stays: it was never a command, and
	// switching must not eat what is in the box.
	require.Equal(t, "/", k.m.prompt.Value())
	for _, r := range "usr/bin/env" {
		k.press(t, string(r))
	}
	assert.False(t, k.m.prompt.Open(), "and typing never opens another")
	assert.Equal(t, "/usr/bin/env", k.m.prompt.Value())
}

// The bar is there whatever has focus, so the key that switches it
// must be too. Intercepted before owner() for exactly this reason.
func TestShell_ShiftTabFlipsFromEveryOwner(t *testing.T) {
	setups := map[string]func(*Model){
		"input":   func(*Model) {},
		"busy":    func(m *Model) { m.waiting = true },
		"history": func(m *Model) { m.nav.focus = focusHistory },
		"output":  func(m *Model) { m.nav.focus = focusOutput },
		"confirm": func(m *Model) { m.mode = modeConfirm },
	}
	for name, setup := range setups {
		t.Run(name, func(t *testing.T) {
			k := newKeyed(t)
			setup(&k.m)
			owner := k.m.owner()

			k.press(t, "shift+tab")
			assert.True(t, k.m.prompt.shell, "from owner %v", owner)
			k.press(t, "shift+tab")
			assert.False(t, k.m.prompt.shell, "and back")
		})
	}
}

// A question outranks the input bar: switching must not dismiss one
// that is waiting on an answer.
func TestShell_ShiftTabDoesNotAnswerAPendingQuestion(t *testing.T) {
	k := newKeyed(t)
	k.m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "clean"})
	k.m.apply(event.ApprovalAsked{ToolCall: uuid.Must(uuid.NewV7()), Tool: "bash"})
	require.Equal(t, modeConfirm, k.m.mode)

	k.press(t, "shift+tab")
	assert.Equal(t, modeConfirm, k.m.mode, "the call is still waiting")
	assert.NotNil(t, k.m.asking)
	assert.True(t, k.m.prompt.shell, "but the bar switched underneath it")
}

// Theirs stops before the Turn does: they are watching it, and the
// Turn is not what they just pressed esc about.
func TestShell_EscapeStopsTheirCommandBeforeTheTurn(t *testing.T) {
	k := newKeyed(t)
	turn, shell := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	k.m.apply(event.TurnStarted{Turn: turn, N: 1, Prompt: "look"})
	k.m.apply(event.UserCommandStarted{UserCommand: shell, Command: "sleep 45", Runner: "host"})

	k.press(t, "esc")
	got, ok := k.intent(t).(event.CancelCommand)
	require.True(t, ok, "esc must cancel the command, not abort the Turn")
	assert.Equal(t, uuid.Nil, got.UserCommand, "whichever is running, which is what esc knows")

	k.m.apply(event.UserCommandEnded{UserCommand: shell, Result: event.Result{Err: "stopped by the human"}})
	k.press(t, "esc")
	_, ok = k.intent(t).(event.Abort)
	assert.True(t, ok, "and once it has gone, esc aborts the Turn again")
}

func TestApply_ACommandBetweenTurnsGetsItsOwnBlock(t *testing.T) {
	shell := uuid.Must(uuid.NewV7())
	m := feed(t,
		event.UserCommandStarted{UserCommand: shell, Command: "git status", Runner: "sandbox"},
		event.UserCommandEnded{UserCommand: shell, Result: event.Result{Stdout: "clean\n"}},
	)
	require.Len(t, m.blocks, 1)
	b := m.blocks[0]
	assert.True(t, b.shell, "not a Turn: nothing was asked for")
	assert.Zero(t, b.n, "and it has no number to undo by")

	require.Len(t, b.rows, 1)
	r := b.rows[0]
	assert.True(t, r.human)
	assert.Equal(t, "git status", r.command)
	assert.False(t, r.running, "UserCommandEnded settles it")
	require.NotNil(t, r.result)
	assert.Equal(t, "clean\n", r.result.Stdout)
}

// One sitting, not ten blocks: a burst of looking is one thing.
func TestApply_ABurstOfCommandsSharesOneBlock(t *testing.T) {
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m := feed(t,
		event.UserCommandStarted{UserCommand: a, Command: "ls"},
		event.UserCommandEnded{UserCommand: a},
		event.UserCommandStarted{UserCommand: b, Command: "pwd"},
		event.UserCommandEnded{UserCommand: b},
	)
	require.Len(t, m.blocks, 1)
	assert.Len(t, m.blocks[0].rows, 2)
}

// In the Turn it interrupted, because a block of its own would appear
// after rows that happened before it.
func TestApply_ACommandDuringATurnLandsInThatTurn(t *testing.T) {
	turn, call, shell := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m := feed(t,
		event.TurnStarted{Turn: turn, N: 1, Prompt: "find it"},
		event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "find ."}},
		event.UserCommandStarted{UserCommand: shell, Command: "ps aux"},
	)
	require.Len(t, m.blocks, 1, "no second block opened")
	rows := m.blocks[0].rows
	require.Len(t, rows, 2)
	assert.False(t, rows[0].human)
	assert.True(t, rows[1].human, "and it reads after the call it interrupted")
}

func TestShell_TheRowSaysItWasTheirs(t *testing.T) {
	shell := uuid.Must(uuid.NewV7())
	m := feed(t,
		event.UserCommandStarted{UserCommand: shell, Command: "git status", Runner: "host"},
		event.UserCommandEnded{UserCommand: shell, Result: event.Result{Stdout: "clean\n"}},
	)
	m.sizeViewport()

	lines, _ := m.historyLines()
	assert.Contains(t, stripStyle(strings.Join(lines, "\n")), "$",
		"a row the human ran carries its own mark wherever it landed")
}

// In PowerShell the row is marked with the prompt it was typed at.
func TestShell_APowerShellRowSaysPS(t *testing.T) {
	shell := uuid.Must(uuid.NewV7())
	m := feed(t,
		event.UserCommandStarted{UserCommand: shell, Command: "Get-ChildItem", Runner: "host"},
		event.UserCommandEnded{UserCommand: shell, Result: event.Result{Stdout: "a.txt\n"}},
	)
	m.prompt.powershell = true
	m.sizeViewport()

	lines, _ := m.historyLines()
	got := stripStyle(strings.Join(lines, "\n"))
	assert.Contains(t, got, "PS>")
	assert.NotContains(t, got, "$", "not the sh mark")
}

// Nothing ever ends a shell block, so anything keyed on "not ended"
// would treat it as live for the rest of the session.
func TestShell_AShellBlockDoesNotReadAsALiveTurn(t *testing.T) {
	m := feed(t, event.UserCommandStarted{UserCommand: uuid.Must(uuid.NewV7()), Command: "ls"})
	require.Len(t, m.blocks, 1)
	assert.Equal(t, styleMuted, m.railStyle(m.blocks[0]))
}

func TestShell_AShellBlockIsNotAnUndoTarget(t *testing.T) {
	m := feed(t, event.UserCommandStarted{UserCommand: uuid.Must(uuid.NewV7()), Command: "rm -rf build"})
	_, err := m.undoTarget("/undo 0")
	assert.Equal(t, "no request 0", err, "a command is not a request, so it is not undone")

	_, err = m.undoTarget("/undo")
	assert.Equal(t, "nothing to undo", err)
}

// Driven through Update rather than asserted on the predicate,
// because the guard in route is what actually decides.
func TestShell_TheSpinnerTurnsForTheirCommandToo(t *testing.T) {
	m := feed(t, event.UserCommandStarted{UserCommand: uuid.Must(uuid.NewV7()), Command: "sleep 45"})
	require.False(t, m.waiting, "no Turn is running, so waiting alone would freeze it")

	before := m.spinner.View()
	next, cmd := m.update(spinner.TickMsg{})
	assert.NotEqual(t, before, next.spinner.View(), "the frame moved on")
	assert.NotNil(t, cmd, "and the chain carries on by itself")
}

// The border is the one thing on screen that is visible without
// looking at the glyph, so it has to say which mode the bar is in.
func TestShell_TheInputBorderSaysWhichModeItIsIn(t *testing.T) {
	m := feed(t)
	require.Equal(t, focusInput, m.nav.focus)
	assert.Equal(t, palette.Accent, m.inputBorder(), "a request gets the ordinary accent")

	m.prompt.SetShell(true)
	assert.Equal(t, palette.Caution, m.inputBorder(), "a command is marked as one")

	m.nav.focus = focusHistory
	assert.Equal(t, palette.Border, m.inputBorder(), "an unfocused bar is neutral in either mode")
}

// Marking it amber is pointless if the palette draws the two the
// same, and a theme is free to change either.
func TestShell_TheTwoBordersDifferInEveryTheme(t *testing.T) {
	for name, th := range theme.Themes {
		t.Run(name, func(t *testing.T) {
			assert.NotEqual(t, th.Accent, th.Caution)
		})
	}
}

// The other two panes keep the rule they always had: the mode belongs
// to the input bar and nothing else.
func TestShell_OnlyTheInputBarChangesColour(t *testing.T) {
	assert.Equal(t, palette.Accent, paneBorder(true))
	assert.Equal(t, palette.Border, paneBorder(false))
}

// The helper is only worth having if View asks it. Rendered rather
// than asserted on the function, because the wiring is what breaks.
func TestShell_TheRenderedBorderFollowsTheMode(t *testing.T) {
	m := feed(t)
	m.sizeViewport()
	inPrompt := inputTopBorder(t, m)

	m.prompt.SetShell(true)
	inShell := inputTopBorder(t, m)

	assert.NotEqual(t, inPrompt, inShell, "the input island is drawn in the mode's colour")
	assert.Equal(t, stripStyle(inPrompt), stripStyle(inShell), "and nothing but the colour moved")
}

// Nothing subscribes UserCommandEnded to the judge, on purpose, so a row
// that said "judging…" would wait for a verdict that never comes.
func TestShell_TheRowNeverPromisesAVerdict(t *testing.T) {
	shell := uuid.Must(uuid.NewV7())
	m := feed(t,
		event.UserCommandStarted{UserCommand: shell, Command: "git status"},
		event.UserCommandEnded{UserCommand: shell, Result: event.Result{Stdout: "clean\n"}},
	)
	m.sizeViewport()

	lines, _ := m.historyLines()
	assert.NotContains(t, stripStyle(strings.Join(lines, "\n")), "judging",
		"no judge will ever see this row")
}

// A command that never finished must not read as one that worked: its
// exit code is 0 because it never got to exit.
func TestShell_ACancelledCommandDoesNotReadAsASuccess(t *testing.T) {
	shell := uuid.Must(uuid.NewV7())
	m := feed(t,
		event.UserCommandStarted{UserCommand: shell, Command: "sleep 45"},
		event.UserCommandEnded{UserCommand: shell, Result: event.Result{Err: "stopped by the human"}},
	)
	m.sizeViewport()

	lines, _ := m.historyLines()
	drawn := stripStyle(strings.Join(lines, "\n"))
	assert.Contains(t, drawn, "✗")
	assert.NotContains(t, drawn, "✓")
}

// The container snapshot covers a command run inside it exactly as it
// covers a tool call, and the undo page must not claim otherwise.
func TestShell_TheirCommandIsUndoneWithTheTurnItRanIn(t *testing.T) {
	turn, shell := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m := feed(t,
		event.TurnStarted{Turn: turn, N: 1, Prompt: "build it"},
		event.UserCommandStarted{UserCommand: shell, Command: "ls build/", Runner: "sandbox"},
		event.UserCommandEnded{UserCommand: shell},
	)
	reversible, standing := split(m.blocks[0].rows)
	assert.Len(t, reversible, 1, "the snapshot predates it, so it goes back")
	assert.Empty(t, standing, "listing it as standing would promise less than a rollback does")
}

// showWelcome is focused() == nil and Restore moves the cursor onto a
// row, so on resume the seam is the only thing that says so.
func TestResume_HistoryMarksWhereItWasPickedUp(t *testing.T) {
	turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m := feed(t,
		event.TurnStarted{Turn: turn, N: 1, Prompt: "install jq"},
		event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "apt-get install jq"}},
		event.ToolCallEnded{ToolCall: call},
		event.TurnEnded{Turn: turn, Reason: event.EndDone},
		event.SessionResumed{Records: 412, Sandbox: false},
	)
	m.sizeViewport()
	require.False(t, m.showWelcome(),
		"a restored session has rows, so the cursor is on one and the pane is gone")

	require.Len(t, m.blocks, 2)
	seam := m.blocks[1]
	require.NotNil(t, seam.seam)
	assert.Empty(t, seam.rows, "a seam holds nothing; it marks a place")

	line := seamLineOf(t, m)
	assert.Contains(t, line, "resumed · 412 records")
	assert.Contains(t, line, "host", "the mode it came back as")
	assert.NotContains(t, line, "…", "and it fits the pane it is drawn in")
}

// A count on the header could only ever describe the last one.
func TestResume_EverySeamIsKept(t *testing.T) {
	m := feed(t,
		event.SessionResumed{Records: 10, Sandbox: true},
		event.SessionResumed{Records: 40, Sandbox: true},
	)
	m.sizeViewport()
	require.Len(t, m.blocks, 2)

	drawn := stripStyle(strings.Join(mustLines(t, m), "\n"))
	assert.Contains(t, drawn, "resumed · 10 records")
	assert.Contains(t, drawn, "resumed · 40 records")
}

// A seam is not a request, so /undo must not offer it one.
func TestResume_ASeamIsNotAnUndoTarget(t *testing.T) {
	m := feed(t, event.SessionResumed{Records: 3})
	_, err := m.undoTarget("/undo 0")
	assert.Equal(t, "no request 0", err)
}

// A PowerShell session prompts as pwsh does, so the human writes PowerShell.
func TestShell_PromptsAsTheShellDoes(t *testing.T) {
	for _, tt := range []struct {
		powershell bool
		want       string
	}{{false, "$ "}, {true, "PS> "}} {
		p := newPrompt(tt.powershell)
		p.Resize(60)
		assert.Contains(t, stripStyle(p.View("")), "❯ ", "a request looks the same either way")
		p.SetShell(true)
		p.Resize(60)
		assert.Contains(t, stripStyle(p.View("")), tt.want)
		p.SetValue(strings.Repeat("x", 200))
		p.Resize(60)
		assert.Greater(t, p.input.Height(), 1, "a long command still wraps")
	}
}

// inputTopBorder is the input island's top edge, which is the last one
// drawn: the panes above it are rendered side by side before it.
func inputTopBorder(t *testing.T, m Model) string {
	t.Helper()
	lines := strings.Split(m.baseView(), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "╭") {
			return lines[i]
		}
	}
	t.Fatal("no input island was drawn")
	return ""
}

// seamLineOf is the drawn seam, so an assertion about its width is
// not satisfied by a truncated command three rows above it.
func seamLineOf(t *testing.T, m Model) string {
	t.Helper()
	for _, l := range mustLines(t, m) {
		if strings.Contains(l, "resumed") {
			return stripStyle(l)
		}
	}
	t.Fatal("no seam was drawn")
	return ""
}

func mustLines(t *testing.T, m Model) []string {
	t.Helper()
	lines, _ := m.historyLines()
	require.NotEmpty(t, lines)
	return lines
}
