package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// The judge stops a Turn from its own goroutine after a network call, so
// its verdict on one request can land in the next.
func TestDispatch_IntentsForAnEndedTurnAreDropped(t *testing.T) {
	r := newRig(t, []model.Reply{{Text: "one done"}, {Requests: []event.ToolRequest{bashCall("c1", "slow")}}})
	first := r.run("one").Turn

	r.runner.mu.Lock()
	r.runner.hold = make(chan struct{})
	r.runner.mu.Unlock()
	r.bus.Publish(event.SubmitPrompt{Text: "two"})
	r.await(event.ToolCallStartedKind)

	r.bus.Publish(event.RequestStop{Turn: first, Reason: "stale"})
	r.bus.Publish(event.Abort{Turn: first})
	close(r.runner.hold)

	end := r.awaitNth(event.TurnEndedKind, 2).(event.TurnEnded)
	assert.Equal(t, event.EndDone, end.Reason, "a verdict on the first request ended the second")
}

// Reset mid-Turn aborts at once rather than queueing behind a blocked
// Call, and the transcript the model sees is really gone afterwards.
func TestReset_MidTurnAbortsAndForgets(t *testing.T) {
	r := newRig(t, []model.Reply{{Requests: []event.ToolRequest{bashCall("c1", "sleep 60")}}})
	r.runner.mu.Lock()
	r.runner.hold = make(chan struct{})
	r.runner.mu.Unlock()

	r.bus.Publish(event.SubmitPrompt{Text: "go"})
	r.await(event.ToolCallStartedKind)
	start := time.Now()
	r.bus.Publish(event.ResetSession{})

	end := r.await(event.TurnEndedKind).(event.TurnEnded)
	assert.Equal(t, event.EndAborted, end.Reason)
	r.await(event.SessionResetKind)
	assert.Less(t, time.Since(start), time.Second, "the reset waited on the blocked call")
	assert.Empty(t, r.eng.Transcript())

	r.run("fresh")
	msgs := r.eng.Transcript()
	require.NotEmpty(t, msgs)
	assert.Equal(t, "fresh", msgs[0].Content, "the next request starts from nothing")
}

// Undo and reset are facts, so a resumed session does not bring back
// what the human took back.
func TestRestore_HonoursUndoAndReset(t *testing.T) {
	t.Run("a rollback", func(t *testing.T) {
		snap := &snapRunner{fakeRunner: &fakeRunner{out: "ok\n"}}
		r := rigWith(t, event.New(), &fakeModel{replies: []model.Reply{{Text: "a"}, {Text: "b"}}}, snap)
		r.run("kept")
		second := r.run("undone").Turn
		r.bus.Publish(event.RequestRollback{Turn: second})
		r.await(event.RolledBackKind)

		fresh := New(event.New(), &fakeModel{}, tool.Standard(), fakeSelector{&fakeRunner{}})
		defer fresh.unsub()
		fresh.Restore(r.records())
		assert.Equal(t, r.eng.Transcript(), fresh.Transcript())
		assert.Equal(t, 1, fresh.turns, "the next request is numbered 2 again")
	})

	t.Run("a reset", func(t *testing.T) {
		r := newRig(t, []model.Reply{{Text: "a"}, {Text: "b"}})
		r.run("forgotten")
		r.bus.Publish(event.ResetSession{})
		r.await(event.SessionResetKind)
		r.run("kept")

		fresh := New(event.New(), &fakeModel{}, tool.Standard(), fakeSelector{&fakeRunner{}})
		defer fresh.unsub()
		fresh.Restore(r.records())
		assert.Equal(t, r.eng.Transcript(), fresh.Transcript())
		assert.Equal(t, 1, fresh.turns)
	})
}

// An Abort landing while the model ignores ctx is read by the Turn
// goroutine and written by the dispatcher. -race is what checks it.
func TestAbort_WhileTheModelIgnoresItsContext(t *testing.T) {
	r := newRig(t, nil)
	stall := make(chan struct{})
	r.model.mu.Lock()
	r.model.stall = stall
	r.model.replies = []model.Reply{{Requests: []event.ToolRequest{bashCall("c1", "ls")}}}
	r.model.mu.Unlock()

	r.bus.Publish(event.SubmitPrompt{Text: "go"})
	r.await(event.StepStartedKind)
	r.bus.Publish(event.Abort{})
	time.Sleep(20 * time.Millisecond)
	close(stall)

	end := r.await(event.TurnEndedKind).(event.TurnEnded)
	assert.Equal(t, event.EndAborted, end.Reason)
	assert.Empty(t, r.runner.commands(), "an aborted Step ran its call anyway")
	answered(t, r.eng)
}

// A judge saying bash only reads is an estimate. What the tool declares
// decides parallelism, so an unknown command still runs alone.
func TestStep_AJudgeCannotMakeAShellCommandParallel(t *testing.T) {
	judge := fixedJudge{risk: event.Risk{Mutability: event.MutRead, ScopeRisk: -1}}
	r := newRig(t, []model.Reply{{Requests: []event.ToolRequest{
		bashCall("c1", "sed -i s/a/b/ x"), bashCall("c2", "cat x"),
	}}}, WithJudge(judge, 0.5))
	r.runner.mu.Lock()
	r.runner.hold = make(chan struct{})
	r.runner.mu.Unlock()

	r.bus.Publish(event.SubmitPrompt{Text: "go"})
	r.await(event.ToolCallStartedKind)
	time.Sleep(100 * time.Millisecond)
	assert.Len(t, r.of(event.ToolCallStartedKind), 1, "two shell commands ran at once")
	close(r.runner.hold)
	r.await(event.TurnEndedKind)
}

// Reads run together only with reads beside them, never ahead of a write
// asked for first.
func TestStep_AReadNeverJumpsAnEarlierWrite(t *testing.T) {
	write := event.ToolRequest{ID: "w", Name: "write_file", Args: map[string]any{"path": "x", "content": "new"}}
	r := newRig(t, []model.Reply{{Requests: []event.ToolRequest{readCall("r0", "y"), write, readCall("r1", "x")}}})
	r.run("write then read")

	reg := tool.Standard()
	w, err := reg.Prepare("write_file", write.Args)
	require.NoError(t, err)
	rd, err := reg.Prepare("read_file", map[string]any{"path": "x"})
	require.NoError(t, err)
	ran := r.runner.commands()
	require.Len(t, ran, 3)
	assert.Equal(t, []string{w.Command, rd.Command}, ran[1:], "the read saw the file before the write")
}

// An abort during the question is not the human saying no, and the
// next Step must not be told never to try it again.
func TestApproval_AnAbortIsNotADecline(t *testing.T) {
	r := newRig(t, []model.Reply{{Requests: []event.ToolRequest{bashCall("c1", "rm -rf build")}}})
	r.bus.Publish(event.SubmitPrompt{Text: "clean"})
	r.await(event.ApprovalAskedKind)
	r.bus.Publish(event.Abort{})
	r.await(event.TurnEndedKind)

	for _, m := range r.eng.Transcript() {
		if m.Role == event.RoleTool {
			assert.Contains(t, m.Content, "aborted")
			assert.NotContains(t, m.Content, "declined")
		}
	}
}

// Every proposed row gets an end, however its Call was settled.
func TestStep_EveryProposedCallEnds(t *testing.T) {
	r := newRig(t, []model.Reply{{Requests: []event.ToolRequest{
		{ID: "bad", Name: "bash", Err: "arguments are not JSON"},
		bashCall("ok", "ls"),
		bashCall("ok", "pwd"),
		bashCall("x", "rm -rf build"),
	}}})
	r.bus.Publish(event.SubmitPrompt{Text: "go"})
	ask := r.await(event.ApprovalAskedKind).(event.ApprovalAsked)
	r.bus.Publish(event.ResolveApproval{ToolCall: ask.ToolCall, Approved: false})
	r.await(event.TurnEndedKind)

	proposed, ended := r.of(event.ToolCallProposedKind), r.of(event.ToolCallEndedKind)
	assert.Len(t, ended, len(proposed))
	assert.Equal(t, []string{"ls"}, r.runner.commands())
}

// Two calls a model gave one id each get their own answer.
func TestStep_ADuplicateIDIsRefusedNotMerged(t *testing.T) {
	r := newRig(t, []model.Reply{{Requests: []event.ToolRequest{bashCall("same", "ls"), bashCall("same", "pwd")}}})
	r.run("go")
	var answers []string
	for _, m := range r.eng.Transcript() {
		if m.Role == event.RoleTool {
			answers = append(answers, m.Content)
		}
	}
	require.Len(t, answers, 2)
	assert.Contains(t, answers[0], "Exit code 0")
	assert.Contains(t, answers[1], "own id")
}

func TestTurn_AModelErrorEndsTheTurnAsAnError(t *testing.T) {
	r := newRig(t, nil)
	r.model.mu.Lock()
	r.model.err = errors.New("endpoint said 500")
	r.model.mu.Unlock()
	end := r.run("go")
	assert.Equal(t, event.EndError, end.Reason)
	assert.Contains(t, end.Summary, "500")
}

// A Turn with no checkpoint says so when it starts, not when undo fails.
func TestCheckpoint_AFailedSnapshotIsSaid(t *testing.T) {
	r := rigWith(t, event.New(), &fakeModel{replies: []model.Reply{{Text: "a"}}}, &brokenSnap{fakeRunner: &fakeRunner{}})
	r.run("go")
	var said bool
	for _, n := range r.of(event.NoticeKind) {
		said = said || strings.Contains(n.(event.Notice).Text, "no checkpoint")
	}
	assert.True(t, said)
	assert.Empty(t, r.of(event.CheckpointTakenKind))
}

// The caller closes the output channel, so a Runner failing before it
// sent anything costs no grace period.
func TestExecute_ARunnerErrorEndsTheCallAtOnce(t *testing.T) {
	r := rigWith(t, event.New(), &fakeModel{replies: []model.Reply{{Requests: []event.ToolRequest{bashCall("c1", "ls")}}}}, failingRunner{})
	start := time.Now()
	r.run("go")
	assert.Less(t, time.Since(start), time.Second)
	ended := r.await(event.ToolCallEndedKind).(event.ToolCallEnded)
	assert.Contains(t, ended.Result.Err, "already exists")
}

// A mark compaction folded into its note cannot be rewound to.
func TestTranscript_TruncatingToAnEatenMarkDoesNothing(t *testing.T) {
	var tr transcript
	for i := range 4 {
		tr.user(i, strings.Repeat("x", 400))
	}
	eaten := 3
	tr.protect = 4
	_, note := tr.compact(context.Background(), 1, nil)
	require.NotEmpty(t, note)
	before := len(tr.msgs)
	assert.False(t, tr.truncate(eaten))
	assert.Len(t, tr.msgs, before, "the summary note went with it")
}

type brokenSnap struct{ *fakeRunner }

func (brokenSnap) Snapshot(context.Context) (string, error) { return "", errors.New("disk full") }
func (brokenSnap) Rollback(context.Context, string) error   { return nil }

type failingRunner struct{}

func (failingRunner) Run(context.Context, string, chan<- capture.StreamEvent) (capture.Result, error) {
	return capture.Result{}, errors.New("sandbox: create task: already exists")
}
