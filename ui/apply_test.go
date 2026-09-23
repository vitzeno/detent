package ui

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// The whole point of the reducer: a test drives the UI with a
// sequence of events and no harness at all. No fake driver, no
// goroutines, no channels.
func feed(t *testing.T, evs ...event.Event) Model {
	t.Helper()
	m := New(context.Background(), event.New(), SessionInfo{Model: "m"})
	m.layout.width, m.layout.height = 120, 40
	for _, e := range evs {
		m.apply(e)
	}
	return m
}

func aTurn(prompt string) (event.ID, []event.Event) {
	turn := event.NewID()
	return turn, []event.Event{event.TurnStarted{Turn: turn, N: 1, Prompt: prompt}}
}

func TestApply_BuildsABlockPerRequest(t *testing.T) {
	turn, evs := aTurn("count the go files")
	call := event.NewID()
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
	bad, good := event.NewID(), event.NewID()
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
	a, b := event.NewID(), event.NewID()
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
	call := event.NewID()
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
	call := event.NewID()
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
	second := event.NewID()
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
	call := event.NewID()
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
		event.CallEnded{Call: event.NewID()},
		event.CallJudged{Call: event.NewID()},
		event.OutputChunk{Call: event.NewID(), Line: "x"},
		event.CheckpointTaken{Turn: event.NewID()},
		event.TurnEnded{Turn: event.NewID()},
	)
	assert.Empty(t, m.blocks)
}
