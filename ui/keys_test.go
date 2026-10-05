package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/island"
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
		// esc after closing the finder declined a question nobody had seen.
		{"approval by esc", event.ApprovalAsked{ToolCall: uuid.Must(uuid.NewV7()), Tool: "bash"}, "esc"},
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

func TestApproval_SecondQuestionWaitsBehindTheFirst(t *testing.T) {
	k := queued(t, "rm -rf a", "rm -rf b")
	first, second := k.m.asked[0].ToolCall, k.m.asked[1].ToolCall
	assert.Contains(t, ansi.Strip(k.m.confirmBox()), "1 of 2")
	assert.Contains(t, ansi.Strip(k.m.confirmBox()), "rm -rf a")

	k.press(t, "y")
	assert.Equal(t, event.ResolveApproval{ToolCall: first, Approved: true}, k.intent(t))
	require.NotNil(t, k.m.asking())
	assert.Equal(t, second, k.m.asking().ToolCall)
	assert.Equal(t, modeConfirm, k.m.mode, "the next question goes straight up")
	assert.NotContains(t, ansi.Strip(k.m.confirmBox()), "1 of", "one left needs no count")
	assert.False(t, k.m.waiting, "nothing runs while a question is up")
}

// The y meant for a question that vanished must not answer the one that replaced it.
func TestApproval_RemovedHeadDoesNotPassItsKeyToTheNext(t *testing.T) {
	k := queued(t, "rm -rf a", "rm -rf b")
	k.m.apply(event.ToolCallEnded{ToolCall: k.m.asked[0].ToolCall})

	k.press(t, "y")
	k.noIntent(t)
	assert.Equal(t, modeConfirm, k.m.mode)
}

// A question arriving behind the one being read must not reset its scroll or its settle.
func TestApproval_ArrivalBehindTheHeadKeepsItsScrollAndSettle(t *testing.T) {
	k := queued(t, tallCommand("rm -rf a"))
	k.press(t, "pgdown")
	top, at := k.m.confirm.top, k.m.askedAt
	require.Positive(t, top)

	k.m.apply(asked(uuid.Must(uuid.NewV7()), "rm -rf b"))
	assert.Equal(t, top, k.m.confirm.top, "the scroll was kept")
	assert.Equal(t, at, k.m.askedAt, "and so was the settle")
}

// Having read one tall command to its end says nothing about the next.
func TestApproval_ReadingOneCommandDoesNotReadTheNext(t *testing.T) {
	k := queued(t, tallCommand("rm -rf a"), tallCommand("rm -rf b"))
	for range 100 {
		k.press(t, "pgdown")
	}
	require.True(t, k.m.confirmReady())
	k.press(t, "y")
	k.intent(t)
	k.settle()

	assert.False(t, k.m.confirmReady(), "the next command starts unread")
	k.press(t, "y")
	k.noIntent(t)
}

func TestApproval_DoubleEscDoesNotDeclineTheNext(t *testing.T) {
	k := queued(t, "rm -rf a", "rm -rf b")
	first, second := k.m.asked[0].ToolCall, k.m.asked[1].ToolCall

	k.press(t, "esc")
	assert.Equal(t, event.ResolveApproval{ToolCall: first, Approved: false}, k.intent(t))
	k.press(t, "esc")
	k.noIntent(t)
	require.NotNil(t, k.m.asking())
	assert.Equal(t, second, k.m.asking().ToolCall)
}

// The bordered box must still fit, and still open its gate, on a small terminal.
func TestApprovalBox_FitsNarrowAndShortScreens(t *testing.T) {
	why := strings.TrimSpace(strings.Repeat("deletes files outside the work tree ", 4))
	for _, size := range [][2]int{{20, 20}, {40, 20}, {80, 24}, {120, 40}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			k := queued(t, tallCommand("rm -rf ../build"))
			k.m.asked[0].Rationale = why
			k.m.layout.width, k.m.layout.height = size[0], size[1]
			k.m.sizeViewport()
			for range 200 {
				k.press(t, "pgdown")
			}
			screen := ansi.Strip(k.m.baseView())
			assert.LessOrEqual(t, strings.Count(screen, "\n")+1, size[1], "the frame fits the screen")
			assert.GreaterOrEqual(t, k.m.nav.histHeight, minBodyRows, "the panes keep their floor")
			// The box only: below 56 columns the panes' own floor is wider than the screen.
			for l := range strings.SplitSeq(ansi.Strip(k.m.confirmBox()), "\n") {
				assert.Equal(t, size[0], ansi.StringWidth(l), "the box spans the screen exactly")
			}
			require.True(t, k.m.confirmReady())
			assert.Contains(t, screen, "../build", "ready only once the last line is on screen")
		})
	}
}

// Stopping a child throws its work away but for its report, so a stray x
// only arms it, and any other key disarms it.
func TestApply_StopKeySendsStopAgentForTheFocusedSpawn(t *testing.T) {
	k := newKeyed(t)
	sp := spawnIn(&k.m, "explore")
	openAgents(t, k)

	k.press(t, "x")
	k.noIntent(t)
	assert.Contains(t, k.m.notice.text, "x again to stop explore")
	k.press(t, "down")
	k.press(t, "x")
	k.noIntent(t)

	k.press(t, "x")
	assert.Equal(t, event.StopAgent{Agent: sp.agent}, k.intentOf(t, event.StopAgentKind))
}

// A child's question waits in the agents block, not in the box, so it never
// takes the keys from whatever the human is doing.
func TestApproval_AChildsQuestionTakesNoKeys(t *testing.T) {
	k := newKeyed(t)
	sp := spawnIn(&k.m, "explore")
	childAsks(&k.m, sp, "cat secrets.env")

	assert.Equal(t, modeInput, k.m.mode)
	assert.Nil(t, k.m.asking(), "the box shows only the root's questions")
	assert.Contains(t, ansi.Strip(k.m.statusBar()), "agents 1 · 1 !", "a blocked child shows from anywhere")
	assert.Contains(t, ansi.Strip(k.m.statusBar()), "an agent is waiting on you")
}

func TestInspector_OpensOnTheBlockedCall(t *testing.T) {
	k := inspecting(t)
	assert.Equal(t, modeInspector, k.m.mode)
	require.NotNil(t, k.m.askOf(k.m.inspectorRow()))
	assert.Contains(t, ansi.Strip(k.m.inspectorBox()), "! blocked")
	assert.Contains(t, ansi.Strip(k.m.inspectorBox()), "y run · n decline")
	frame := strings.Split(k.m.View().Content, "\n")
	assert.Len(t, frame, k.m.layout.height, "the frame fills the screen and no more")
}

func TestInspector_OpensOnTheReportWhenDone(t *testing.T) {
	k := newKeyed(t)
	sp := spawnIn(&k.m, "explore")
	k.m.apply(event.AgentEnded{Agent: sp.agent, Reason: event.AgentDone})
	k.m.apply(event.ToolCallEnded{ToolCall: sp.spawn, Result: event.Result{Stdout: "the report"}})
	openAgents(t, k)
	assert.Equal(t, sp.spawn, k.m.inspectorRow().id)
}

// Answered in the inspector, a question keeps the box's guards: the settle,
// then every line of the command read.
func TestInspector_AnswersWithTheBoxsGuards(t *testing.T) {
	k := inspecting(t)
	k.press(t, "y")
	k.noIntent(t)

	k.m.insp.shownAt = time.Now().Add(-2 * time.Hour)
	k.press(t, "y")
	got, ok := k.intent(t).(event.ResolveApproval)
	require.True(t, ok)
	assert.True(t, got.Approved)
	assert.Nil(t, k.m.askOf(k.m.inspectorRow()), "an answered question leaves the agent unblocked")
}

func TestInspector_ATallCommandMustBeReadFirst(t *testing.T) {
	k := inspecting(t, tallCommand("rm -rf ~/important"))
	k.m.insp.shownAt = time.Now().Add(-2 * time.Hour)
	k.press(t, "y")
	k.noIntent(t)
	assert.Contains(t, k.m.notice.text, "read to the end")

	k.press(t, "tab")
	for range 100 {
		k.press(t, "pgdown")
	}
	assert.Contains(t, ansi.Strip(k.m.inspectorBox()), "rm -rf ~/important")
	k.press(t, "y")
	assert.IsType(t, event.ResolveApproval{}, k.intent(t))
}

// The root's question waits for the inspector to close, as for the finder,
// so a key typed there never answers it.
func TestInspector_ARootQuestionWaitsUntilItCloses(t *testing.T) {
	k := inspecting(t)
	k.m.apply(asked(uuid.Must(uuid.NewV7()), "rm -rf build"))
	assert.Equal(t, modeInspector, k.m.mode, "the question did not take the screen")
	k.press(t, "n")
	assert.NotNil(t, k.m.asking(), "n in the inspector answered the root's question")

	k.press(t, "esc")
	assert.Equal(t, modeConfirm, k.m.mode, "it comes up once the inspector closes")
}

// esc leaves the inspector for the pane it was opened from, not the input.
func TestInspector_EscReturnsToWhereItWasOpened(t *testing.T) {
	k := newKeyed(t)
	spawnIn(&k.m, "explore")
	openAgents(t, k)
	k.press(t, "esc")
	assert.Equal(t, modeInput, k.m.mode)
	assert.Equal(t, focusHistory, k.m.nav.focus, "back in history, where a opened it")
}

// esc leaves the inspector, and only a second one stops the request.
func TestInspector_EscFromItNeverAborts(t *testing.T) {
	k := newKeyed(t)
	spawnIn(&k.m, "explore")
	openAgents(t, k)
	k.press(t, "esc")
	assert.Equal(t, modeInput, k.m.mode)
	k.noIntent(t)
}

func TestSlash_AgentsOpensTheInspector(t *testing.T) {
	k := newKeyed(t)
	k.m, _ = k.m.showAgents("/agents")
	assert.Equal(t, modeInput, k.m.mode)
	assert.Contains(t, k.m.notice.text, "no subagents")

	first := spawnIn(&k.m, "first")
	second := spawnAlso(&k.m, first.turn, "second")
	k.m, _ = k.m.showAgents("/agents")
	assert.Equal(t, second.agent, k.m.insp.agent.id, "the newest when none waits")

	k.m, _ = k.m.closeInspector()
	childAsks(&k.m, first, "cat x")
	k.m, _ = k.m.showAgents("/agents")
	assert.Equal(t, first.agent, k.m.insp.agent.id, "one waiting on the human first")

	k.m, _ = k.m.closeInspector()
	k.m, _ = k.m.showAgents("/agents second")
	assert.Equal(t, second.agent, k.m.insp.agent.id)
	k.m, _ = k.m.closeInspector()
	k.m, _ = k.m.showAgents("/agents nobody")
	assert.Contains(t, k.m.notice.text, "no subagent named nobody")
}

// A resumed session replays its subagents' facts, so their work can still be
// looked into after detent quit.
func TestSlash_AgentsBrowsesAResumedSessionsWork(t *testing.T) {
	turn, spawn, agent, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	stored := asRecords([]event.Event{
		event.SessionStarted{Session: uuid.Must(uuid.NewV7()), Subagents: true},
		event.TurnStarted{Turn: turn, N: 1, Prompt: "trace login"},
		event.ToolCallProposed{ToolCall: spawn, Tool: spawnTool, Args: map[string]any{"task": "trace"}},
		event.AgentStarted{Agent: agent, ToolCall: spawn, Name: "trace", Task: "trace login"},
		event.StepStarted{Turn: turn, Step: uuid.Must(uuid.NewV7()), N: 1, Agent: agent},
		event.ToolCallProposed{ToolCall: call, Tool: "grep", Args: map[string]any{"pattern": "login"}, Agent: agent},
		event.ToolCallEnded{ToolCall: call, Result: event.Result{Stdout: "auth.go:12: func login"}},
		event.AgentEnded{Agent: agent, Reason: event.AgentDone},
		event.ToolCallEnded{ToolCall: spawn, Result: event.Result{Stdout: "login is in auth.go:12"}},
		event.TurnEnded{Turn: turn, Reason: event.EndDone},
	})
	k := newKeyed(t)
	k.m = k.m.Restore(stored)
	k.m.sizeViewport()

	k.m, _ = k.m.showAgents("/agents")
	require.Equal(t, modeInspector, k.m.mode)
	box := ansi.Strip(k.m.inspectorBox())
	assert.Contains(t, box, "login is in auth.go:12", "it opens on the report")
	k.press(t, "up")
	assert.Contains(t, ansi.Strip(k.m.inspectorBox()), "auth.go:12: func login", "and every call is there")
}

func TestInspector_EscClosesAndAnswersNothing(t *testing.T) {
	k := inspecting(t)
	k.m.insp.shownAt = time.Now().Add(-2 * time.Hour)
	k.press(t, "esc")
	assert.Equal(t, modeInput, k.m.mode)
	k.noIntent(t)
	assert.Len(t, k.m.childAsks, 1, "the question still waits")
}

func TestInspector_ArrowsMoveBetweenAgentsInSpawnOrder(t *testing.T) {
	k := newKeyed(t)
	first := spawnIn(&k.m, "first")
	second := spawnAlso(&k.m, first.turn, "second")
	openAgents(t, k)
	assert.Equal(t, second.agent, k.m.insp.agent.id, "a opens on the newest")
	k.press(t, "left")
	assert.Equal(t, first.agent, k.m.insp.agent.id)
	k.press(t, "left")
	assert.Equal(t, first.agent, k.m.insp.agent.id, "the first stays the first")
	k.press(t, "right")
	assert.Equal(t, second.agent, k.m.insp.agent.id)
}

func TestInspector_StopKeyStopsThisAgent(t *testing.T) {
	k := inspecting(t)
	k.press(t, "x")
	k.noIntent(t)
	k.press(t, "x")
	assert.Equal(t, event.StopAgent{Agent: k.m.insp.agent.id}, k.intentOf(t, event.StopAgentKind))
}

func TestInspector_DefusesWhatTheChildWrote(t *testing.T) {
	k := newKeyed(t)
	sp := spawnIn(&k.m, "explore")
	k.m.apply(event.ModelText{Turn: sp.turn, Text: "look\x1b]52;c;cm0gLXJmIH4=\x07 here", Agent: sp.agent})
	openAgents(t, k)
	k.press(t, "down")
	assert.NotContains(t, k.m.inspectorBox(), "\x1b]52", "an escape the child wrote reached the terminal")
}

func TestAgentsBlock_OrdersBlockedFirstAndCapsRows(t *testing.T) {
	k := newKeyed(t)
	sp := spawnIn(&k.m, "a0")
	last := sp
	for i := 1; i < 6; i++ {
		last = spawnAlso(&k.m, sp.turn, fmt.Sprintf("a%d", i))
	}
	childAsks(&k.m, last, "cat x")
	k.m.sizeViewport()

	block := ansi.Strip(strings.Join(k.m.pinnedLines(), "\n"))
	require.Contains(t, block, "a5", "the blocked one is shown, though it started last")
	assert.Less(t, strings.Index(block, "a5"), strings.Index(block, "a0"), "and comes first")
	assert.Contains(t, block, "+2 more", "four rows and a count of the rest")
	assert.NotContains(t, block, "a4")
}

func TestAgentsBlock_UnpinsWhenTheTurnEnds(t *testing.T) {
	k := newKeyed(t)
	sp := spawnIn(&k.m, "explore")
	require.NotEmpty(t, k.m.pinnedLines())
	k.m.apply(event.TurnEnded{Turn: sp.turn, Reason: event.EndDone})
	assert.Empty(t, k.m.pinnedLines())
	assert.Len(t, k.m.rows(), 1, "its spawn row stays in history")
}

func TestAgentsBlock_NoLineWiderThanThePane(t *testing.T) {
	for _, width := range []int{60, 80, 120, 200} {
		k := newKeyed(t)
		k.m.layout.width = width
		sp := spawnIn(&k.m, "a-name-much-longer-than-a-row-has-room-for")
		childAsks(&k.m, sp, "cat x")
		k.m.sizeViewport()
		for _, l := range k.m.historyPaneLines() {
			assert.LessOrEqual(t, lipgloss.Width(l), island.Inner(k.m.layout.histColW), "width %d", width)
		}
	}
}

// The block takes rows from history below it, and following still shows the newest.
func TestHistory_ShrinksToMakeRoomForTheBlock(t *testing.T) {
	k := newKeyed(t)
	sp := spawnIn(&k.m, "explore")
	for i := range 40 {
		k.m.apply(event.ToolCallProposed{ToolCall: uuid.Must(uuid.NewV7()), Tool: "bash",
			Args: map[string]any{"command": fmt.Sprintf("echo %d", i)}})
	}
	k.m.sizeViewport()
	lines := k.m.historyPaneLines()
	assert.Len(t, lines, k.m.nav.histHeight, "the block and history share the pane")
	block := len(k.m.pinnedLines())
	assert.Contains(t, ansi.Strip(lines[len(lines)-1-block]), "echo 39", "the newest row sits just above the block")
	assert.Contains(t, ansi.Strip(lines[len(lines)-2]), "explore", "and the block ends the pane")
	_ = sp
}

// a opens the inspector straight away, from history or the output pane.
func TestAgents_AKeyOpensTheInspector(t *testing.T) {
	for _, pane := range []focusPane{focusHistory, focusOutput} {
		k := newKeyed(t)
		sp := spawnIn(&k.m, "explore")
		k.m.nav.focus = pane
		k.m.prompt.Blur()
		k.press(t, "a")
		assert.Equal(t, modeInspector, k.m.mode, "from pane %d", pane)
		assert.Equal(t, sp.agent, k.m.insp.agent.id)
	}
}

// The welcome is the boot screen: once something is asked, a request whose
// only rows are its subagents says so instead.
func TestApply_SpawningKeepsTheWelcomeAway(t *testing.T) {
	k := newKeyed(t)
	assert.True(t, k.m.showWelcome())
	spawnIn(&k.m, "explore")
	k.m.sizeViewport()
	assert.False(t, k.m.showWelcome())
	assert.Contains(t, ansi.Strip(strings.Join(k.m.detailLines(), "\n")), "subagents are working")
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
	require.NotNil(t, k.m.asking())

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

// inspecting is the inspector open on a child waiting on cmd, a settle of an
// hour holding every key until a test moves it.
func inspecting(t *testing.T, cmd ...string) *keyed {
	t.Helper()
	was := questionSettle
	questionSettle = time.Hour
	t.Cleanup(func() { questionSettle = was })
	k := newKeyed(t)
	sp := spawnIn(&k.m, "migrate")
	childAsks(&k.m, sp, append(cmd, "psql -c 'DROP COLUMN legacy_id'")[0])
	openAgents(t, k)
	return k
}

// childAsks has sp's child propose cmd and wait on the human for it.
func childAsks(m *Model, sp spawn, cmd string) {
	call := uuid.Must(uuid.NewV7())
	args := map[string]any{"command": cmd}
	m.apply(event.StepStarted{Turn: sp.turn, Step: uuid.Must(uuid.NewV7()), N: 1, Agent: sp.agent})
	m.apply(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: args, Agent: sp.agent})
	m.apply(event.ApprovalAsked{ToolCall: call, Tool: "bash", Args: args, Rationale: "regex: drop column", Agent: sp.agent})
}

// openAgents opens the inspector with a, from history.
func openAgents(t *testing.T, k *keyed) {
	t.Helper()
	k.m.nav.focus = focusHistory
	k.m.prompt.Blur()
	k.m.sizeViewport()
	k.press(t, "a")
	require.Equal(t, modeInspector, k.m.mode)
}

// queued is a Turn with a question per command, the first settled and up. A
// settle of an hour means any question raised later holds back every key.
func queued(t *testing.T, cmds ...string) *keyed {
	t.Helper()
	was := questionSettle
	questionSettle = time.Hour
	t.Cleanup(func() { questionSettle = was })
	k := newKeyed(t)
	k.m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "clean"})
	for _, c := range cmds {
		call := uuid.Must(uuid.NewV7())
		k.m.apply(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": c}})
		k.m.apply(asked(call, c))
	}
	k.m.sizeViewport()
	k.settle()
	return k
}

// settle lets the question up stop holding back keys.
func (k *keyed) settle() { k.m.askedAt = time.Now().Add(-2 * time.Hour) }

// noIntent fails if a key published anything.
func (k *keyed) noIntent(t *testing.T) {
	t.Helper()
	select {
	case rec := <-k.seen:
		t.Fatalf("a key published %s", rec.Event.Kind())
	case <-time.After(50 * time.Millisecond):
	}
}

// tallCommand is a command too tall for the box, ending in tail.
func tallCommand(tail string) string {
	var b strings.Builder
	for i := range 60 {
		fmt.Fprintf(&b, "echo step %d\n", i)
	}
	return b.String() + tail
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
	case "up":
		return tea.KeyUp
	case "left":
		return tea.KeyLeft
	case "right":
		return tea.KeyRight
	case "pgdown":
		return tea.KeyPgDown
	case "pgup":
		return tea.KeyPgUp
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

// Why a call was flagged is shown whole up to a few lines, and the box counts
// those lines, so the frame still fits and the read-to-the-end gate holds.
func TestKeys_ALongRationaleWrapsAndTheBoxStillFits(t *testing.T) {
	k := newKeyed(t)
	// Short, so the panes have no rows left to give the box.
	k.m.layout.height = 20
	why := strings.TrimSpace(strings.Repeat("it deletes files outside the working directory ", 8))
	k.m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "clean"})
	var script strings.Builder
	for i := range 60 {
		fmt.Fprintf(&script, "echo step %d\n", i)
	}
	script.WriteString("rm -rf ../build")
	k.m.apply(event.ApprovalAsked{ToolCall: uuid.Must(uuid.NewV7()), Tool: "bash",
		Args: map[string]any{"command": script.String()}, Rationale: why})
	k.m.sizeViewport()

	lines := k.m.rationaleLines()
	assert.Greater(t, len(lines), 1, "it wraps")
	assert.LessOrEqual(t, len(lines), maxRationale, "and is capped")
	for range 100 {
		k.press(t, "pgdown")
	}
	screen := ansi.Strip(k.m.baseView())
	assert.LessOrEqual(t, strings.Count(screen, "\n")+1, k.m.layout.height, "the frame fits the screen")
	require.True(t, k.m.confirmReady())
	assert.Contains(t, screen, "rm -rf ../build", "ready only once the last line is really on screen")
}
