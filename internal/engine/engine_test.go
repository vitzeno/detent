package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// A Step asking for several Calls runs them all, and the Turn ends when
// the model stops asking.
func TestTurn_MultiCallStep(t *testing.T) {
	r := newRig(t, []model.Reply{{
		Text:  "reading three things",
		Calls: []event.ToolCall{readCall("c1", "a.go"), readCall("c2", "b.go"), bashCall("c3", "git status")},
	}})
	end := r.run("look around")

	assert.Equal(t, event.EndDone, end.Reason)
	assert.Equal(t, "finished", end.Summary)
	assert.Len(t, r.runner.commands(), 3, "every call ran")
	assert.Len(t, r.of(event.CallEndedKind), 3)
	answered(t, r.eng)
}

// Whatever goes wrong, every tool_call id is answered. This is the
// invariant the next Step is built on.
func TestTurn_EveryCallIsAnsweredHoweverItWent(t *testing.T) {
	tests := []struct {
		name  string
		calls []event.ToolCall
		want  []string
	}{
		{
			name:  "a tool that does not exist",
			calls: []event.ToolCall{{ID: "c1", Name: "delete_file", Args: map[string]any{}}, bashCall("c2", "ls")},
			want:  []string{"no tool named", "Exit code"},
		},
		{
			name: "arguments that are not valid JSON",
			calls: []event.ToolCall{
				{ID: "c1", Name: "bash", Args: map[string]any{}, Err: "arguments were not valid JSON: boom"},
				bashCall("c2", "ls"),
			},
			want: []string{"valid JSON", "Exit code"},
		},
		{
			name:  "arguments that fail the schema",
			calls: []event.ToolCall{{ID: "c1", Name: "read_file", Args: map[string]any{"nope": "x"}}},
			want:  []string{"unknown parameter"},
		},
		{
			name:  "a required argument left out",
			calls: []event.ToolCall{{ID: "c1", Name: "read_file", Args: map[string]any{}}},
			want:  []string{"missing required"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, []model.Reply{{Calls: tt.calls}})
			r.run("go")
			answered(t, r.eng)

			msgs := r.eng.Transcript()
			var answers []string
			for _, m := range msgs {
				if m.Role == event.RoleTool {
					answers = append(answers, m.Content)
				}
			}
			require.Len(t, answers, len(tt.calls))
			for i, w := range tt.want {
				assert.Contains(t, answers[i], w)
			}
		})
	}
}

// Declining stops a Call, not a Turn. Its siblings still
// run and the model gets to react.
func TestTurn_DeclineStopsOneCallOnly(t *testing.T) {
	r := newRig(t, []model.Reply{{Calls: []event.ToolCall{
		bashCall("c1", "rm -rf /tmp/x"), // the regex hook flags this
		bashCall("c2", "ls"),
	}}})
	r.bus.Publish(event.SubmitPrompt{Text: "clean up"})

	asked := r.await(event.ApprovalAskedKind).(event.ApprovalAsked)
	assert.True(t, asked.Risk.Dangerous)
	assert.Contains(t, asked.Rationale, "delete")
	r.bus.Publish(event.ResolveApproval{Call: asked.Call, Approved: false})

	end := r.await(event.TurnEndedKind).(event.TurnEnded)
	assert.Equal(t, event.EndDone, end.Reason, "a decline does not end the Turn")
	assert.Equal(t, []string{"ls"}, r.runner.commands(), "the declined call never ran, its sibling did")
	answered(t, r.eng)
	assert.Contains(t, r.eng.Transcript()[2].Content, "declined")
}

// Abort cancels in flight and still completes the Step.
func TestTurn_AbortStillAnswersEveryCall(t *testing.T) {
	r := newRig(t, []model.Reply{{Calls: []event.ToolCall{
		bashCall("c1", "sleep 1"), bashCall("c2", "sleep 2"), bashCall("c3", "sleep 3"),
	}}})
	r.runner.mu.Lock()
	r.runner.hold = make(chan struct{}) // every call blocks
	r.runner.mu.Unlock()

	r.bus.Publish(event.SubmitPrompt{Text: "slow work"})
	started := r.await(event.CallStartedKind)
	require.NotNil(t, started)

	turn := r.of(event.TurnStartedKind)[0].(event.TurnStarted)
	r.bus.Publish(event.Abort{Turn: turn.Turn})

	end := r.await(event.TurnEndedKind).(event.TurnEnded)
	assert.Equal(t, event.EndAborted, end.Reason)
	answered(t, r.eng)
}

// The bound asks rather than stopping.
func TestTurn_BoundAsksAndCanBeContinued(t *testing.T) {
	var replies []model.Reply
	for i := range 6 {
		replies = append(replies, model.Reply{Calls: []event.ToolCall{bashCall("c"+string(rune('0'+i)), "ls")}})
	}
	r := newRig(t, replies, WithMaxSteps(2))
	r.bus.Publish(event.SubmitPrompt{Text: "keep going"})

	first := r.await(event.BoundReachedKind).(event.BoundReached)
	assert.Equal(t, 2, first.Steps)
	r.bus.Publish(event.Continue{Turn: first.Turn, Approved: false})

	end := r.await(event.TurnEndedKind).(event.TurnEnded)
	assert.Equal(t, event.EndBound, end.Reason)
	answered(t, r.eng)
}

func TestTurn_BoundContinuesWhenApproved(t *testing.T) {
	replies := []model.Reply{
		{Calls: []event.ToolCall{bashCall("a", "ls")}},
		{Calls: []event.ToolCall{bashCall("b", "pwd")}},
	}
	r := newRig(t, replies, WithMaxSteps(2))
	r.bus.Publish(event.SubmitPrompt{Text: "go"})

	b := r.await(event.BoundReachedKind).(event.BoundReached)
	r.bus.Publish(event.Continue{Turn: b.Turn, Approved: true})

	end := r.await(event.TurnEndedKind).(event.TurnEnded)
	assert.Equal(t, event.EndDone, end.Reason)
	assert.Contains(t, r.runner.commands(), "pwd", "it carried on past the bound")
}

// RequestStop is advisory: honoured at a boundary, never mid-Step.
// This is how the post-execution judge acts without intercepting.
func TestTurn_RequestStopEndsAtTheNextBoundary(t *testing.T) {
	replies := []model.Reply{
		{Calls: []event.ToolCall{bashCall("a", "first")}},
		{Calls: []event.ToolCall{bashCall("b", "second")}},
	}
	r := newRig(t, replies)
	// Hold the first Call so the stop lands inside a Step, or the fake
	// model finishes the whole Turn before anything is published at it.
	r.runner.mu.Lock()
	r.runner.hold = make(chan struct{})
	r.runner.mu.Unlock()

	r.bus.Publish(event.SubmitPrompt{Text: "go"})
	r.await(event.CallStartedKind)
	turn := r.of(event.TurnStartedKind)[0].(event.TurnStarted)
	r.bus.Publish(event.RequestStop{Turn: turn.Turn, Reason: "the goal looks met"})
	// Settled before the Call is let go: the engine reads intents on
	// its own goroutine, so releasing straight away races the hop.
	r.bus.Settle(3 * time.Second)
	close(r.runner.hold)

	end := r.await(event.TurnEndedKind).(event.TurnEnded)
	assert.Equal(t, event.EndStopped, end.Reason)
	assert.Equal(t, "the goal looks met", end.Summary)
	answered(t, r.eng)
}

// A prompt typed while a Turn runs is steering, not a new Turn.
func TestTurn_PromptMidTurnBecomesANote(t *testing.T) {
	replies := []model.Reply{
		{Calls: []event.ToolCall{bashCall("a", "find .")}},
		{Calls: []event.ToolCall{bashCall("b", "rg x")}},
	}
	r := newRig(t, replies)
	r.runner.mu.Lock()
	r.runner.hold = make(chan struct{})
	r.runner.mu.Unlock()

	r.bus.Publish(event.SubmitPrompt{Text: "search the tree"})
	r.await(event.CallStartedKind)
	r.bus.Publish(event.SubmitPrompt{Text: "use ripgrep, not find"})
	r.bus.Settle(3 * time.Second)
	close(r.runner.hold)

	r.await(event.TurnEndedKind)
	assert.Len(t, r.of(event.TurnStartedKind), 1, "steering must not open a second Turn")

	var seen bool
	for _, m := range r.eng.Transcript() {
		seen = seen || m.Content == "use ripgrep, not find"
	}
	assert.True(t, seen, "the correction must reach the model")
	answered(t, r.eng)
}

func TestTurn_ModelErrorEndsTheTurnCleanly(t *testing.T) {
	r := newRig(t, []model.Reply{{Calls: []event.ToolCall{bashCall("c1", "ls")}}})
	r.model.mu.Lock()
	r.model.replies = nil
	r.model.mu.Unlock()
	end := r.run("go")
	assert.Equal(t, event.EndDone, end.Reason)
	answered(t, r.eng)
}

// A model that stops mid-thought is told to carry on, not taken as done.
func TestTurn_AnUnfinishedReplyIsNudgedOn(t *testing.T) {
	r := newRig(t, []model.Reply{
		{Text: "I could replace insight with", Thinking: true, Stop: "stop"},
		{Text: "half an ans", Stop: "length"},
		{Calls: []event.ToolCall{bashCall("c1", "ls")}},
		{Text: "setting iterations to 30 and", Stop: "error"},
	})
	end := r.run("fix it")

	assert.Equal(t, event.EndDone, end.Reason)
	assert.Equal(t, "finished", end.Summary)
	assert.Equal(t, []string{"ls"}, r.runner.commands())
	notes := 0
	for _, m := range r.model.lastSent() {
		if m.Role == event.RoleUser && m.Content == unfinishedNote {
			notes++
		}
	}
	assert.Equal(t, 3, notes, "a call in between resets the count")
	require.Len(t, r.of(event.NoticeKind), 3, "a nudge is visible, not only in the transcript")
	assert.Contains(t, r.of(event.NoticeKind)[1].(event.Notice).Text, "stop: length")
	assert.Contains(t, r.of(event.NoticeKind)[2].(event.Notice).Text, "stop: error")
	answered(t, r.eng)
}

// The nudge is bounded, so a model that never answers still ends the Turn.
func TestTurn_NudgingGivesUpAfterTheLimit(t *testing.T) {
	var replies []model.Reply
	for range DefaultNudges + 3 {
		replies = append(replies, model.Reply{Stop: "stop"})
	}
	r := newRig(t, replies)
	end := r.run("fix it")

	assert.Equal(t, event.EndDone, end.Reason)
	assert.Len(t, r.of(event.StepEndedKind), DefaultNudges+1)
}

// A panicking Runner must cost one Call, not the session: every
// checkpoint a human could still roll back to lives in the engine.
func TestTurn_PanickingRunnerIsOneFailedCall(t *testing.T) {
	r := newRig(t, []model.Reply{{Calls: []event.ToolCall{bashCall("c1", "boom")}}})
	r.runner.mu.Lock()
	r.runner.pan = true
	r.runner.mu.Unlock()

	end := r.run("go")
	assert.Equal(t, event.EndDone, end.Reason)
	answered(t, r.eng)
	assert.Contains(t, r.eng.Transcript()[2].Content, "panicked")
}

func TestTurn_PublishesTheFactsAFrontEndNeeds(t *testing.T) {
	r := newRig(t, []model.Reply{{Text: "looking", Calls: []event.ToolCall{bashCall("c1", "ls")}}})
	r.run("go")

	for _, k := range []event.Kind{
		event.SessionStartedKind, event.TurnStartedKind, event.StepStartedKind,
		event.ModelTextKind, event.CallProposedKind, event.CallAssessedKind,
		event.CallStartedKind, event.OutputChunkKind, event.CallEndedKind,
		event.StepEndedKind, event.TurnEndedKind,
	} {
		assert.NotEmpty(t, r.of(k), "nothing published %s", k)
	}
}

func TestTurn_CheckpointsOncePerTurn(t *testing.T) {
	bus := event.New()
	snap := &snapRunner{fakeRunner: &fakeRunner{out: "ok\n"}}
	fm := &fakeModel{replies: []model.Reply{
		{Calls: []event.ToolCall{bashCall("a", "one")}},
		{Calls: []event.ToolCall{bashCall("b", "two")}},
	}}
	r := rigWith(t, bus, fm, snap)
	r.run("do several things")

	assert.Len(t, r.of(event.CheckpointTakenKind), 1, "one checkpoint per Turn, not per Call")
	snap.mu.Lock()
	defer snap.mu.Unlock()
	assert.Len(t, snap.taken, 1)
}

func TestEngine_ResetForgetsTheTranscript(t *testing.T) {
	r := newRig(t, []model.Reply{{Calls: []event.ToolCall{bashCall("c1", "ls")}}})
	r.run("go")
	require.NotEmpty(t, r.eng.Transcript())

	r.bus.Publish(event.ResetSession{})
	require.Eventually(t, func() bool { return len(r.eng.Transcript()) == 0 },
		2*time.Second, 10*time.Millisecond)
}

// A front-end cannot ask the registry, so the tool's own answer about
// how to read its output has to travel on the fact.
func TestCallProposed_CarriesTheToolsDeclaredShape(t *testing.T) {
	reg := tool.Standard()
	reg.Register(markdownTool{})
	call := event.ToolCall{ID: "m1", Name: "declares_markdown"}
	r := rigWithTools(t, event.New(), &fakeModel{replies: []model.Reply{{Calls: []event.ToolCall{call}}}},
		&fakeRunner{out: "ok\n"}, reg)
	r.run("go")

	proposed := r.of(event.CallProposedKind)
	require.Len(t, proposed, 1)
	assert.Equal(t, event.RendersMarkdown, proposed[0].(event.CallProposed).Renders,
		"the tool's answer did not reach the fact")
}

// A front-end says what a rollback cannot take back, so it has to be
// told which Calls ran outside the sandbox.
func TestCallProposed_CarriesTheExecutor(t *testing.T) {
	reg := tool.Standard()
	reg.Register(remoteTool{name: "srv__do"})
	r := rigWithTools(t, event.New(),
		&fakeModel{replies: []model.Reply{{Calls: []event.ToolCall{remoteCall("r1")}}}},
		&fakeRunner{out: "ok\n"}, reg, WithInvoker(&fakeInvoker{}))
	r.approve(t)
	r.run("go")

	proposed := r.of(event.CallProposedKind)
	require.Len(t, proposed, 1)
	assert.Equal(t, "srv", proposed[0].(event.CallProposed).Executor,
		"nothing said this Call ran outside the sandbox")
}

// A shell Call must not claim one, or undo would say it cannot
// reverse things it can.
func TestCallProposed_AShellCallHasNoExecutor(t *testing.T) {
	r := newRig(t, []model.Reply{{Calls: []event.ToolCall{bashCall("c1", "ls")}}})
	r.run("go")

	proposed := r.of(event.CallProposedKind)
	require.Len(t, proposed, 1)
	assert.Empty(t, proposed[0].(event.CallProposed).Executor)
}

// No tool ships with an opinion, and inventing one would send ordinary
// command output through a markdown renderer.
func TestCallProposed_LeavesRendersEmptyWhenTheToolHasNoOpinion(t *testing.T) {
	r := newRig(t, []model.Reply{{Calls: []event.ToolCall{bashCall("c1", "ls")}}})
	r.run("go")

	proposed := r.of(event.CallProposedKind)
	require.Len(t, proposed, 1)
	assert.Empty(t, proposed[0].(event.CallProposed).Renders)
}

// A front-end measures a Step's prompt tokens against the budget, so
// the budget has to be on the fact that describes the run.
func TestSessionStarted_CarriesTheContextBudget(t *testing.T) {
	r := newRig(t, nil, WithContextTokens(9_000))
	started := r.await(event.SessionStartedKind).(event.SessionStarted)
	assert.Equal(t, 9_000, started.ContextTokens)
}

func TestSessionStarted_NamesTheInstructionFiles(t *testing.T) {
	r := newRig(t, nil, WithInstructions([]string{"../AGENTS.md", "CLAUDE.md"}))
	started := r.await(event.SessionStartedKind).(event.SessionStarted)
	assert.Equal(t, []string{"../AGENTS.md", "CLAUDE.md"}, started.Instructions)
}

// Unset means the default applies, not that there is no budget: a
// front-end showing "no limit" would be wrong.
func TestSessionStarted_ReportsTheDefaultBudgetWhenUnset(t *testing.T) {
	r := newRig(t, nil)
	started := r.await(event.SessionStartedKind).(event.SessionStarted)
	assert.Equal(t, DefaultContextTokens, started.ContextTokens)
}

// markdownTool stands in for one that knows its output is a document,
// since none of the shipped tools claims a shape today.
type markdownTool struct{}

func (markdownTool) Name() string { return "declares_markdown" }
func (markdownTool) Describe() tool.Spec {
	return tool.Spec{Description: "x", Mutability: event.MutRead, Renders: event.RendersMarkdown}
}
func (markdownTool) Lower(tool.Args) (string, error) { return "true", nil }

// A Turn that changed something is asked once to check its work before it
// ends, and the model's second answer is the one that stands.
func TestTurn_AChangingTurnIsAskedToCheckOnce(t *testing.T) {
	r := newRig(t, []model.Reply{
		{Calls: []event.ToolCall{bashCall("c1", "touch out.txt")}},
		{Text: "made out.txt", Stop: "stop"},
		{Calls: []event.ToolCall{readCall("c2", "out.txt")}},
		{Text: "made out.txt, and checked it", Stop: "stop"},
		{Text: "never asked for", Stop: "stop"},
	}, WithFinishCheck(true))
	end := r.run("make out.txt")

	assert.Equal(t, event.EndDone, end.Reason)
	assert.Equal(t, "made out.txt, and checked it", end.Summary)
	checks := 0
	for _, m := range r.model.lastSent() {
		if m.Role == event.RoleUser && m.Content == finishNote {
			checks++
		}
	}
	assert.Equal(t, 1, checks, "asked once, however the Turn goes on")
	require.Len(t, r.of(event.NoticeKind), 1)
	answered(t, r.eng)
}

// A Turn that only read is answered as it is, with no extra Step.
func TestTurn_AReadOnlyTurnIsNotAskedToCheck(t *testing.T) {
	r := newRig(t, []model.Reply{
		{Calls: []event.ToolCall{readCall("c1", "a.go")}},
		{Text: "a.go holds main", Stop: "stop"},
	}, WithFinishCheck(true))
	end := r.run("what is in a.go")

	assert.Equal(t, "a.go holds main", end.Summary)
	assert.Len(t, r.of(event.StepEndedKind), 2)
	assert.Empty(t, r.of(event.NoticeKind))
}
