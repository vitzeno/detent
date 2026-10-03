package engine

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/model"
)

// A NoteContext published rather than typed as a prompt, which is how
// anything but the input box sends one.

// Idle, the note goes in at once so the next Turn sees it.
func TestNoteContext_LandsWhenNothingIsRunning(t *testing.T) {
	r := newRig(t, nil)

	r.bus.Publish(event.NoteContext{Text: "use ripgrep, not grep"})
	added := r.await(event.AppendedKind).(event.Appended)
	require.Len(t, added.Messages, 1)
	assert.Equal(t, event.RoleUser, added.Messages[0].Role)
	assert.Equal(t, "use ripgrep, not grep", added.Messages[0].Content)
	assert.Equal(t, uuid.Nil, added.Turn, "a note between Turns belongs to none")

	r.run("now search")
	sent := r.model.lastSent()
	require.Len(t, sent, 2)
	assert.Equal(t, "use ripgrep, not grep", sent[0].Content, "the note came first")
	assert.Equal(t, "now search", sent[1].Content)
}

// Mid-Turn it waits for a Step boundary, because a note between an
// assistant's calls and their answers is a Step nothing accepts.
func TestNoteContext_MidTurnWaitsForAStepBoundary(t *testing.T) {
	r := newRig(t, []model.Reply{{Requests: []event.ToolRequest{bashCall("a", "find .")}}})
	r.runner.mu.Lock()
	r.runner.hold = make(chan struct{})
	r.runner.mu.Unlock()

	r.bus.Publish(event.SubmitPrompt{Text: "search the tree"})
	r.await(event.ToolCallStartedKind)
	r.bus.Publish(event.NoteContext{Text: "use ripgrep, not find"})
	r.dispatched()
	close(r.runner.hold)

	r.await(event.TurnEndedKind)
	msgs := r.eng.messages()
	note, step := -1, -1
	for i, m := range msgs {
		switch {
		case m.Content == "use ripgrep, not find":
			note = i
		case m.Role == event.RoleAssistant && len(m.Requests) > 0:
			step = i
		}
	}
	require.GreaterOrEqual(t, note, 0, "the note must reach the model")
	assert.Greater(t, note, step,
		"a note landing before the Step it interrupted reads as one the model ignored")
	answered(t, r.eng)
}

// A note while a Turn runs must not open one, the way a prompt does
// not either: it is context for the Turn in flight.
func TestNoteContext_MidTurnOpensNoTurn(t *testing.T) {
	r := newRig(t, []model.Reply{{Requests: []event.ToolRequest{bashCall("a", "ls")}}})
	r.runner.mu.Lock()
	r.runner.hold = make(chan struct{})
	r.runner.mu.Unlock()

	r.bus.Publish(event.SubmitPrompt{Text: "look around"})
	r.await(event.ToolCallStartedKind)
	r.bus.Publish(event.NoteContext{Text: "in the ui package"})
	r.dispatched()
	close(r.runner.hold)

	r.await(event.TurnEndedKind)
	assert.Len(t, r.of(event.TurnStartedKind), 1)
	assert.Len(t, r.of(event.AppendedKind), 4,
		"prompt, note, the Step, and the closing say")
}

// A resume replays the transcript, then notes what of it is still true,
// then sends the next prompt. The note must sit between the two.
func TestNoteContext_ResumeLandsBetweenTheOldTranscriptAndTheNewPrompt(t *testing.T) {
	r := newRig(t, nil)
	r.eng.Restore([]event.Record{{Ordinal: 1, Event: event.Appended{
		Messages: []event.Message{
			{Role: event.RoleUser, Content: "install jq"},
			{Role: event.RoleAssistant, Content: "installed"},
		}}}})

	r.bus.Publish(event.NoteContext{Text: "[session resumed]\nthat container is gone"})
	r.await(event.AppendedKind)
	r.run("now use jq")

	var got []string
	for _, m := range r.model.lastSent() {
		got = append(got, m.Content)
	}
	assert.Equal(t, []string{
		"install jq", "installed",
		"[session resumed]\nthat container is gone",
		"now use jq",
	}, got)
}
