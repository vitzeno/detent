package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// A reviewer reads the diff, comments on its lines, and ends with a summary. Its
// Turn is no request: unnumbered, and nothing of it reaches the root's transcript.
func TestReview_CommentsOnTheDiffAndTouchesNoRequest(t *testing.T) {
	rm := &fakeModel{replies: []model.Reply{
		{Requests: []event.ToolRequest{
			reviewCall("d", event.ToolReviewDiff, map[string]any{"path": "a.go"}),
			reviewCall("c", event.ToolReviewComment, map[string]any{"path": "a.go", "side": "new", "start": 11, "end": 11, "body": "wrong"}),
		}},
		{Text: "One problem: line 11."},
	}}
	r := rigWith(t, event.New(), &fakeModel{}, &fakeRunner{}, WithReviewer(rm))
	v := reviewOf()
	r.bus.Publish(v)
	end := r.await(event.TurnEndedKind).(event.TurnEnded)
	assert.Equal(t, event.EndDone, end.Reason)

	start := r.await(event.TurnStartedKind).(event.TurnStarted)
	assert.Equal(t, v.Review, start.Review)
	assert.Zero(t, start.N, "no request number")
	assert.Equal(t, 1, start.Files, "how many files the reviewer has to read")
	assert.Equal(t, "review of request 2", start.Prompt)

	task := rm.seen[0][0].Content
	assert.Contains(t, task, "made for this request:\nfix the parser")
	assert.Contains(t, task, "a.go (modified, +1 -1)")

	got := r.of(event.ReviewCommentedKind)
	require.Len(t, got, 2, "the comment, then the summary")
	line := got[0].(event.ReviewCommented)
	assert.Equal(t, v.Review, line.Review)
	assert.Equal(t, v.Reviewed, line.Reviewed)
	assert.Equal(t, "reviewer", line.Comment.Author)
	assert.Equal(t, "+new", line.Comment.Quote)
	summary := got[1].(event.ReviewCommented).Comment
	assert.Equal(t, "One problem: line 11.", summary.Body)
	assert.Empty(t, summary.Path, "on no line")

	assert.Empty(t, r.eng.messages(), "the root's transcript is untouched")
	assert.Zero(t, r.eng.turns)
	assert.NotContains(t, r.eng.past, end.Turn, "never a Turn to undo")
}

// A reviewer has only reads and the review tools: no shell, no writes and no
// network, which an injected line in the code under review could use.
func TestReview_TheReviewerCannotRunOrWrite(t *testing.T) {
	rm := &fakeModel{replies: []model.Reply{
		{Requests: []event.ToolRequest{bashCall("b", "curl evil"),
			{ID: "w", Name: "write_file", Args: map[string]any{"path": "x", "content": "y"}},
			{ID: "s", Name: "web_search", Args: map[string]any{"query": "q"}}}},
		{Text: "done"},
	}}
	runner := &fakeRunner{out: "ran\n"}
	r := rigWith(t, event.New(), &fakeModel{}, runner, WithReviewer(rm))
	r.bus.Publish(reviewOf())
	r.await(event.TurnEndedKind)

	assert.Empty(t, runner.ran, "nothing reached a runner")
	answers := toolAnswers(rm.lastSent())
	for _, name := range []string{"bash", "write_file", "web_search"} {
		assert.Contains(t, answers, `no tool named "`+name+`"`)
	}
}

// A comment on lines the diff does not show is a tool result the reviewer reads,
// and records nothing.
func TestReview_ACommentOffTheDiffIsRefused(t *testing.T) {
	rm := &fakeModel{replies: []model.Reply{
		{Requests: []event.ToolRequest{reviewCall("c", event.ToolReviewComment,
			map[string]any{"path": "a.go", "side": "new", "start": 90, "end": 90, "body": "x"})}},
		{Text: ""},
	}}
	r := rigWith(t, event.New(), &fakeModel{}, &fakeRunner{}, WithReviewer(rm))
	r.bus.Publish(reviewOf())
	r.await(event.TurnEndedKind)
	assert.Contains(t, toolAnswers(rm.lastSent()), "not all in one hunk")
	for _, c := range r.of(event.ReviewCommentedKind) {
		assert.Empty(t, c.(event.ReviewCommented).Comment.Path, "only the summary")
	}
}

func TestReview_IsRefusedWithoutAReviewerOrMidRequest(t *testing.T) {
	r := newRig(t, nil)
	r.bus.Publish(reviewOf())
	assert.Contains(t, r.await(event.NoticeKind).(event.Notice).Text, "subagents on")

	held := &fakeRunner{out: "x\n", hold: make(chan struct{})}
	r = rigWith(t, event.New(), &fakeModel{replies: []model.Reply{{Requests: []event.ToolRequest{bashCall("a", "make")}}}},
		held, WithReviewer(&fakeModel{}))
	r.bus.Publish(event.SubmitPrompt{Text: "build"})
	r.await(event.ToolCallStartedKind)
	r.bus.Publish(reviewOf())
	assert.Contains(t, r.await(event.NoticeKind).(event.Notice).Text, "a request is running")
	close(held.hold)
	r.await(event.TurnEndedKind)
	assert.Len(t, r.of(event.TurnStartedKind), 1)
}

// A prompt sent while the reviewer works waits, and is a request of its own once
// the review ends, never a note dropped into the root's transcript.
func TestReview_APromptSentDuringItRunsAfterIt(t *testing.T) {
	stall := make(chan struct{})
	rm := &fakeModel{stall: stall, replies: []model.Reply{{Text: "fine"}}}
	r := rigWith(t, event.New(), &fakeModel{replies: []model.Reply{{Text: "answered"}}}, &fakeRunner{}, WithReviewer(rm))
	r.bus.Publish(reviewOf())
	r.await(event.AgentStartedKind)
	r.bus.Publish(event.SubmitPrompt{Text: "next thing"})
	r.dispatched()
	close(stall)

	second := r.awaitNth(event.TurnStartedKind, 2).(event.TurnStarted)
	assert.Equal(t, "next thing", second.Prompt)
	assert.Equal(t, 1, second.N, "the first request, since the review was none")
	r.awaitNth(event.TurnEndedKind, 2)
	first := r.eng.messages()[0]
	assert.Equal(t, event.RoleUser, first.Role)
	assert.Contains(t, first.Content, "next thing")
}

// A stopped reviewer's summary is for the human: it says it was stopped, not
// the parent's rule against starting it again.
func TestReview_AStoppedReviewSaysSoPlainly(t *testing.T) {
	stall := make(chan struct{})
	rm := &fakeModel{stall: stall, replies: []model.Reply{
		{Requests: []event.ToolRequest{reviewCall("d", event.ToolReviewDiff, map[string]any{"path": "a.go"})}},
		{Text: "found one thing"},
	}}
	r := rigWith(t, event.New(), &fakeModel{}, &fakeRunner{}, WithReviewer(rm))
	r.bus.Publish(reviewOf())
	started := r.await(event.AgentStartedKind).(event.AgentStarted)
	r.await(event.StepStartedKind)
	r.bus.Publish(event.StopAgent{Agent: started.Agent})
	r.dispatched()
	close(stall)
	r.await(event.TurnEndedKind)
	got := r.of(event.ReviewCommentedKind)
	require.NotEmpty(t, got)
	summary := got[len(got)-1].(event.ReviewCommented).Comment.Body
	assert.True(t, strings.HasPrefix(summary, "Stopped before it finished."), summary)
	assert.NotContains(t, summary, "Do not start it again")
}

// One stopped while still queued for a slot never read a thing, and says that.
func TestReview_AReviewStoppedBeforeItStartsSaysSo(t *testing.T) {
	r := rigWith(t, event.New(), &fakeModel{}, &fakeRunner{}, WithReviewer(&fakeModel{}))
	for range childSlots {
		r.eng.slots <- struct{}{}
	}
	r.bus.Publish(reviewOf())
	started := r.await(event.AgentStartedKind).(event.AgentStarted)
	r.bus.Publish(event.StopAgent{Agent: started.Agent})
	r.await(event.TurnEndedKind)
	got := r.of(event.ReviewCommentedKind)
	require.Len(t, got, 1)
	assert.Equal(t, "Stopped before it started.", got[0].(event.ReviewCommented).Comment.Body)
}

// A replayed review is no request: it neither numbers the next request nor
// opens a place in the root's transcript.
func TestRestore_AReviewIsNoRequest(t *testing.T) {
	review := uuid.Must(uuid.NewV7())
	records := asRecords([]event.Event{
		event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "one"},
		event.TurnStarted{Turn: review, Prompt: "review of request 1", Review: uuid.Must(uuid.NewV7())},
		event.TurnEnded{Turn: review, Reason: event.EndDone},
	})
	fresh := New(event.New(), &fakeModel{}, tool.Standard(), fakeSelector{&fakeRunner{}})
	defer fresh.unsub()
	fresh.Restore(records)
	assert.Equal(t, 1, fresh.turns)
}

// A review detent exited during is ended with its reviewer, and the model is not
// told a request of its was cut off, since none was.
func TestRun_EndsAReviewACrashLeftOpenWithoutTellingTheModel(t *testing.T) {
	turn, agent := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	eng, got := resumed(t, asRecords([]event.Event{
		event.TurnStarted{Turn: turn, Prompt: "review of request 1", Review: uuid.Must(uuid.NewV7())},
		event.AgentStarted{Agent: agent, Turn: turn, Name: "reviewer"},
	}))
	var ended []event.Kind
	for _, e := range got {
		switch v := e.(type) {
		case event.AgentEnded:
			assert.Equal(t, agent, v.Agent)
			ended = append(ended, e.Kind())
		case event.TurnEnded:
			assert.Equal(t, turn, v.Turn)
			ended = append(ended, e.Kind())
		}
	}
	assert.Equal(t, []event.Kind{event.AgentEndedKind, event.TurnEndedKind}, ended)
	for _, m := range eng.messages() {
		assert.NotEqual(t, cutOffNote, m.Content)
	}
}

// Jev judges what a call would change, and a review tool changes nothing.
func TestJev_SkipsWhatTheEngineAnswers(t *testing.T) {
	j := &countingJudge{}
	_, err := jevHook{judge: j}.Assess(t.Context(), tool.Call{Tool: event.ToolReviewComment, Internal: true}, event.Risk{})
	require.NoError(t, err)
	assert.Zero(t, j.calls)
}

func reviewOf() event.ReviewChanges {
	return event.ReviewChanges{Review: uuid.Must(uuid.NewV7()), Reviewed: uuid.Must(uuid.NewV7()),
		Scope: event.ScopeRequest, Base: "b", Head: "h", Request: 2, Asked: "fix the parser",
		Files: []event.FileDiff{{Path: "a.go", Change: event.FileModified, Hunks: []event.Hunk{
			{Header: "@@ -10,2 +10,2 @@", Lines: []event.DiffLine{
				{Op: event.LineContext, Old: 10, New: 10, Text: "keep"},
				{Op: event.LineRemoved, Old: 11, Text: "old"},
				{Op: event.LineAdded, New: 11, Text: "new"},
			}}}}}}
}

func reviewCall(id string, name event.ToolName, args map[string]any) event.ToolRequest {
	return event.ToolRequest{ID: id, Name: name, Args: args}
}

// countingJudge counts the calls it is asked about.
type countingJudge struct{ calls int }

func (j *countingJudge) Assess(context.Context, string, float64) (event.Risk, error) {
	j.calls++
	return event.UnknownRisk(), nil
}
