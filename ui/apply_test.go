package ui

import (
	"context"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// The whole point of the reducer: a test drives the UI with a
// sequence of events and no harness at all. No fake driver, no
// goroutines, no channels.
func feed(t *testing.T, evs ...event.Event) Model {
	t.Helper()
	m := New(context.Background(), event.New(), SessionInfo{})
	m.layout.width, m.layout.height = 120, 40
	for _, e := range evs {
		m.apply(e)
	}
	return m
}

func aTurn(prompt string) (uuid.UUID, []event.Event) {
	turn := uuid.Must(uuid.NewV7())
	return turn, []event.Event{event.TurnStarted{Turn: turn, N: 1, Prompt: prompt}}
}

func TestApply_BuildsABlockPerRequest(t *testing.T) {
	turn, evs := aTurn("count the go files")
	call := uuid.Must(uuid.NewV7())
	m := feed(t, append(evs,
		event.CheckpointTaken{Turn: turn, Snapshot: "snap-a"},
		event.CallProposed{Call: call, Tool: "bash", Args: map[string]any{"command": "ls"}},
		event.CallStarted{Call: call, Runner: "host"},
		event.CallEnded{Call: call, Result: event.Result{Stdout: "a.go\nb.go\n"}},
		event.ModelText{Turn: turn, Text: "there are two"},
		event.TurnEnded{Turn: turn, Reason: event.EndDone, Summary: "there are two",
			Usage: event.Usage{PromptTokens: 10, CompletionTokens: 5}},
	)...)

	require.Len(t, m.blocks, 1)
	b := m.blocks[0]
	assert.Equal(t, "count the go files", b.prompt)
	assert.True(t, b.undoable, "a checkpoint makes it undoable")
	assert.True(t, b.ended)
	assert.Equal(t, event.EndDone, b.end)

	require.Len(t, b.rows, 2, "the call and the model's own words")
	assert.Equal(t, "ls", b.rows[0].command)
	assert.False(t, b.rows[0].running)
	assert.True(t, b.rows[0].ok())
	assert.Equal(t, "there are two", b.rows[1].prose)

	assert.Equal(t, 1, m.calls)
	assert.Equal(t, 15, m.tokens)
	assert.Zero(t, m.errors)
	assert.Nil(t, m.cur, "the block closes")
}

func TestApply_CountsWhatStatusReports(t *testing.T) {
	turn, evs := aTurn("go")
	bad, good := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m := feed(t, append(evs,
		event.StepEnded{Turn: turn},
		event.CallProposed{Call: bad, Tool: "bash", Args: map[string]any{"command": "false"}},
		event.CallEnded{Call: bad, Result: event.Result{ExitCode: 1}},
		event.CallProposed{Call: good, Tool: "bash", Args: map[string]any{"command": "true"}},
		event.CallEnded{Call: good, Result: event.Result{}},
		event.StepEnded{Turn: turn},
	)...)

	assert.Equal(t, 2, m.calls)
	assert.Equal(t, 1, m.errors, "a non-zero exit counts")
	assert.Equal(t, 2, m.steps)
}

// Live output is routed by call, because parallel calls interleave.
func TestApply_DemultiplexesLiveOutput(t *testing.T) {
	_, evs := aTurn("read two files")
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m := feed(t, append(evs,
		event.CallProposed{Call: a, Tool: "read_file", Args: map[string]any{"path": "a.go"}},
		event.CallProposed{Call: b, Tool: "read_file", Args: map[string]any{"path": "b.go"}},
		event.CallStarted{Call: a}, event.CallStarted{Call: b},
		event.OutputChunk{Call: a, Line: "from a"},
		event.OutputChunk{Call: b, Line: "from b"},
		event.OutputChunk{Call: a, Line: "also a"},
	)...)

	rows := m.rows()
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"from a", "also a"}, rows[0].live)
	assert.Equal(t, []string{"from b"}, rows[1].live)
	assert.Equal(t, "read_file path=a.go", rows[0].command, "a non-bash call reads as tool(k=v)")
}

func TestApply_LiveOutputIsBounded(t *testing.T) {
	_, evs := aTurn("noisy")
	call := uuid.Must(uuid.NewV7())
	evs = append(evs, event.CallProposed{Call: call, Tool: "bash", Args: map[string]any{"command": "yes"}})
	for range maxLiveLines * 2 {
		evs = append(evs, event.OutputChunk{Call: call, Line: "noise"})
	}
	m := feed(t, evs...)
	r := m.rows()[0]
	assert.Len(t, r.live, maxLiveLines, "a runaway command cannot eat memory")
	assert.Positive(t, r.dropped, "and says how much it lost")
}

func TestApply_ApprovalOpensAndClosesTheQuestion(t *testing.T) {
	turn, evs := aTurn("clean up")
	call := uuid.Must(uuid.NewV7())
	m := feed(t, append(evs,
		event.CallProposed{Call: call, Tool: "bash", Args: map[string]any{"command": "rm -rf build"}},
		event.CallAssessed{Call: call, Risk: event.Risk{Dangerous: true, Note: "recursive delete"}},
		event.ApprovalAsked{Call: call, Tool: "bash",
			Args: map[string]any{"command": "rm -rf build"}, Rationale: "recursive delete"},
	)...)

	require.NotNil(t, m.asking)
	assert.Equal(t, modeConfirm, m.mode)
	assert.False(t, m.waiting, "the spinner stops while a human is asked")
	assert.Contains(t, m.confirmBox(), "rm -rf build", "the literal command is what is approved")
	assert.Contains(t, m.confirmBox(), "recursive delete")

	m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone})
	assert.Nil(t, m.asking)
	assert.Equal(t, modeInput, m.mode)
}

func TestApply_BoundPausesForAnAnswer(t *testing.T) {
	turn, evs := aTurn("big job")
	m := feed(t, append(evs, event.BoundReached{Turn: turn, Steps: 50})...)
	require.NotNil(t, m.bound)
	assert.False(t, m.waiting)
	m.mode = modeBound
	assert.Contains(t, m.questionBox(), "50 steps")
}

func TestApply_RollbackDropsTheTurnAndEverythingAfter(t *testing.T) {
	first, firstEvs := aTurn("one")
	second := uuid.Must(uuid.NewV7())
	m := feed(t, append(firstEvs,
		event.TurnEnded{Turn: first, Reason: event.EndDone},
		event.TurnStarted{Turn: second, N: 2, Prompt: "two"},
		event.TurnEnded{Turn: second, Reason: event.EndDone},
	)...)
	require.Len(t, m.blocks, 2)

	m.apply(event.RolledBack{Turn: first})
	assert.Empty(t, m.blocks, "undoing the first drops the second with it")
}

func TestApply_NoticeReachesTheStatusFlash(t *testing.T) {
	m := feed(t, event.Notice{Level: "error", Text: "containerd is unreachable"})
	assert.Equal(t, "containerd is unreachable", m.notice.text)
	assert.True(t, m.notice.bad)

	m.apply(event.Notice{Level: "info", Text: "session reset"})
	assert.False(t, m.notice.bad)
}

func TestApply_JudgementExpandsWhatNeedsAttention(t *testing.T) {
	_, evs := aTurn("go")
	call := uuid.Must(uuid.NewV7())
	m := feed(t, append(evs,
		event.CallProposed{Call: call, Tool: "bash", Args: map[string]any{"command": "make"}},
		event.CallEnded{Call: call, Result: event.Result{ExitCode: 2, Stderr: "boom"}},
		event.CallJudged{Call: call, Status: "hard_failure", RenderKind: "errors",
			Attention: 0.95, FromJudge: true},
	)...)
	r := m.rows()[0]
	require.NotNil(t, r.post)
	assert.Equal(t, "errors", r.kind())
	assert.True(t, r.expanded, "something needing attention opens itself")
}

// An event about a call the UI never saw must not panic: the bus can
// deliver out of order after a reset.
func TestApply_UnknownIdsAreIgnored(t *testing.T) {
	m := feed(t,
		event.CallEnded{Call: uuid.Must(uuid.NewV7())},
		event.CallJudged{Call: uuid.Must(uuid.NewV7())},
		event.OutputChunk{Call: uuid.Must(uuid.NewV7()), Line: "x"},
		event.CheckpointTaken{Turn: uuid.Must(uuid.NewV7())},
		event.TurnEnded{Turn: uuid.Must(uuid.NewV7())},
	)
	assert.Empty(t, m.blocks)
}

// The design's central claim: a front-end that is a projection needs
// no second code path to rebuild one. Restore is a loop over apply.
func TestRestore_RebuildsHistoryFromTheStream(t *testing.T) {
	turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	facts := []event.Event{
		event.SessionStarted{Session: uuid.Must(uuid.NewV7()), Model: "m", MaxSteps: 50},
		event.TurnStarted{Turn: turn, N: 1, Prompt: "count the files"},
		event.CheckpointTaken{Turn: turn, Snapshot: "gone-with-the-container"},
		event.CallProposed{Call: call, Tool: "bash", Args: map[string]any{"command": "ls"}},
		event.CallEnded{Call: call, Result: event.Result{Stdout: "a.go\n"}},
		event.CallJudged{Call: call, Status: "clean_success", RenderKind: "file_listing", FromJudge: true},
		event.ModelText{Turn: turn, Text: "one file"},
		event.TurnEnded{Turn: turn, Reason: event.EndDone, Summary: "one file",
			Usage: event.Usage{PromptTokens: 20, CompletionTokens: 5}},
	}

	m := New(context.Background(), event.New(), SessionInfo{})
	m.layout.width, m.layout.height = 120, 40
	m = m.Restore(asRecords(facts))

	require.Len(t, m.blocks, 1)
	b := m.blocks[0]
	assert.Equal(t, "count the files", b.prompt)
	assert.True(t, b.ended)
	assert.Equal(t, event.EndDone, b.end)
	require.Len(t, b.rows, 2, "the call and the model's words")
	assert.Equal(t, "ls", b.rows[0].command)
	assert.Equal(t, "file_listing", b.rows[0].kind())

	assert.Equal(t, 1, m.calls)
	assert.Equal(t, 25, m.tokens)
	assert.True(t, m.Idle(), "a restored session is not mid-request")

	// The checkpoint died with the container, so this Turn is not
	// offered for undo.
	assert.False(t, b.undoable, "a resumed Turn has no checkpoint to restore")
}

func asRecords(facts []event.Event) []event.Record {
	out := make([]event.Record, len(facts))
	for i, e := range facts {
		out[i] = event.Record{Ordinal: uint64(i + 1), Event: e}
	}
	return out
}

// /sessions asks over the bus rather than reaching into a store, so
// ui keeps importing nothing under internal/.
func TestSessions_AreAskedForAndFolded(t *testing.T) {
	bus := event.New()
	asked, unsub := bus.Subscribe(event.Only(event.ListSessionsKind))
	defer unsub()

	m := New(context.Background(), bus, SessionInfo{})
	m.layout.width, m.layout.height = 120, 40
	next, cmd := m.listSessions("/sessions")
	m = next.(Model)
	require.NotNil(t, cmd)
	cmd()

	assert.Equal(t, panelSessions, m.panel.open)
	select {
	case rec := <-asked:
		assert.Equal(t, event.ListSessionsKind, rec.Event.Kind())
	case <-time.After(2 * time.Second):
		t.Fatal("/sessions never asked for a listing")
	}

	mine := uuid.Must(uuid.NewV7())
	m.apply(event.SessionStarted{Session: mine})
	m.apply(event.SessionsListed{Sessions: []event.SessionSummary{
		{ID: mine, Started: time.Now().UTC(), Events: 12},
		{ID: uuid.Must(uuid.NewV7()), Started: time.Now().UTC(), Events: 3},
	}})

	lines := strings.Join(m.sessionLines(), "\n")
	assert.Contains(t, lines, mine.String())
	assert.Contains(t, lines, "12 events")
	assert.Contains(t, lines, "-resume", "and says how to use one")
}

// The session id is the one thing you need to resume this run later,
// and there was no way to see it from inside the TUI.
func TestStatus_ShowsTheSessionID(t *testing.T) {
	m := New(context.Background(), event.New(), SessionInfo{})
	m.layout.width, m.layout.height = 120, 40
	mine := uuid.Must(uuid.NewV7())
	m.apply(event.SessionStarted{Session: mine, MaxSteps: 50})

	assert.Contains(t, strings.Join(m.statusLines(), "\n"), mine.String())
}

// A session nothing is writing down cannot be resumed, and finding
// that out at resume time is too late.
func TestStatus_SaysWhenNothingIsRecording(t *testing.T) {
	for _, recorded := range []bool{true, false} {
		m := New(context.Background(), event.New(), SessionInfo{})
		m.layout.width, m.layout.height = 120, 40
		m.apply(event.SessionStarted{Session: uuid.Must(uuid.NewV7()), Recorded: recorded})

		got := stripANSI(strings.Join(m.statusLines(), "\n"))
		if recorded {
			assert.Contains(t, got, "resumable")
			assert.NotContains(t, got, "cannot be resumed")
		} else {
			assert.Contains(t, got, "cannot be resumed")
		}
	}
}

// The description comes off the fact, so the log and the panes cannot
// disagree about what ran.
func TestStatus_ReadsTheRunOffTheFact(t *testing.T) {
	m := New(context.Background(), event.New(), SessionInfo{})
	m.layout.width, m.layout.height = 120, 40
	m.apply(event.SessionStarted{
		Session: uuid.Must(uuid.NewV7()), Model: "a-model", Sandbox: true,
		MaxSteps: 42, Recorded: true, Resumed: 9,
	})

	got := stripANSI(strings.Join(m.statusLines(), "\n"))
	assert.Contains(t, got, "a-model")
	assert.Contains(t, got, "sandbox")
	assert.Contains(t, got, "42")
	assert.Contains(t, got, "9 records")
	assert.Equal(t, "sandbox", m.runMode())
}

// A name is how a listing stops being a wall of uuids, and it is the
// one thing in the header a human writes.
func TestRename_PublishesTheIntent(t *testing.T) {
	bus := event.New()
	asked, unsub := bus.Subscribe(event.Only(event.RenameSessionKind))
	defer unsub()

	mine := uuid.Must(uuid.NewV7())
	m := New(context.Background(), bus, SessionInfo{})
	m.layout.width, m.layout.height = 120, 40
	m.apply(event.SessionStarted{Session: mine, Recorded: true})

	next, cmd := m.renameSession("/rename the sandbox bug")
	require.NotNil(t, cmd)
	cmd()

	select {
	case rec := <-asked:
		got := rec.Event.(event.RenameSession)
		assert.Equal(t, mine, got.Session)
		assert.Equal(t, "the sandbox bug", got.Name)
	case <-time.After(2 * time.Second):
		t.Fatal("/rename published nothing")
	}
	_ = next
}

// Naming a session nothing records would not keep, so say so rather
// than appear to work.
func TestRename_RefusedWhenNothingIsRecording(t *testing.T) {
	bus := event.New()
	asked, unsub := bus.Subscribe(event.Only(event.RenameSessionKind))
	defer unsub()

	m := New(context.Background(), bus, SessionInfo{})
	m.layout.width, m.layout.height = 120, 40
	m.apply(event.SessionStarted{Session: uuid.Must(uuid.NewV7()), Recorded: false})

	next, cmd := m.renameSession("/rename doomed")
	assert.Nil(t, cmd, "nothing is published")
	assert.True(t, next.(Model).notice.bad, "and it says why")

	select {
	case <-asked:
		t.Fatal("a rename was published for a session nothing records")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRename_NeedsAName(t *testing.T) {
	m := New(context.Background(), event.New(), SessionInfo{})
	m.layout.width, m.layout.height = 120, 40
	m.apply(event.SessionStarted{Recorded: true})
	next, cmd := m.renameSession("/rename   ")
	assert.Nil(t, cmd)
	assert.Contains(t, next.(Model).notice.text, "usage")
}

// The UI must not claim a rename worked: only the store knows, and
// its reply is what the human should read.
func TestRename_ClaimsNothing(t *testing.T) {
	m := New(context.Background(), event.New(), SessionInfo{})
	m.layout.width, m.layout.height = 120, 40
	m.apply(event.SessionStarted{Session: uuid.Must(uuid.NewV7()), Recorded: true})

	next, _ := m.renameSession("/rename the sandbox bug")
	assert.Empty(t, next.(Model).notice.text, "the store says whether it took")
}

// Resuming replays the original run's facts, and a notice says
// something just happened. Announcing old ones is a lie about now.
func TestRestore_DoesNotFlashHistoryAsNews(t *testing.T) {
	turn := uuid.Must(uuid.NewV7())
	records := []event.Record{
		{Event: event.SessionStarted{Model: "m", ContextTokens: 24_000}},
		{Event: event.TurnStarted{Turn: turn, N: 1, Prompt: "go"}},
		{Event: event.Compacted{Turn: turn, Dropped: 223, Note: "summary"}},
		{Event: event.Notice{Level: "info", Text: "named test"}},
		{Event: event.TurnEnded{Turn: turn, Reason: event.EndDone}},
	}

	m := New(t.Context(), event.New(), SessionInfo{}).Restore(records)
	assert.Empty(t, m.notice.text, "a replayed notice was shown as if it just happened")

	// What the facts mean still lands: only the flash is suppressed.
	assert.Len(t, m.blocks, 1)
	assert.Equal(t, 24_000, m.run.ContextTokens)

	// And a live fact after the replay still flashes.
	m.apply(event.Notice{Level: "info", Text: "this one is now"})
	assert.Equal(t, "this one is now", m.notice.text)
}
