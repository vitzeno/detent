package engine

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// The gate on persistence: everything the transcript holds must be
// reconstructable from the facts, with no formatting reproduced.
func TestAppended_RebuildsTheTranscriptExactly(t *testing.T) {
	r := newRig(t, []model.Reply{
		{Text: "looking", Requests: []event.ToolRequest{
			bashCall("c1", "ls"), readCall("c2", "a.go")}},
		{Text: "and one more", Requests: []event.ToolRequest{bashCall("c3", "pwd")}},
	})
	r.run("do the thing")

	live := r.eng.messages()
	require.NotEmpty(t, live)

	rebuilt := rebuild(t, r.of(event.AppendedKind))
	assert.Equal(t, live, rebuilt.messages(),
		"a replay of Appended must reproduce the transcript byte for byte")
	wellFormed(t, rebuilt.messages())
}

// Compaction rewrites the front, so a live session and its rebuild
// differ in content. The mark still has to resolve to the same place.
func TestAppended_RebuildResolvesAMarkTakenAfterCompaction(t *testing.T) {
	big := strings.Repeat("x", 1500)
	var replies []model.Reply
	for i := range 6 {
		replies = append(replies, model.Reply{
			Text:     big,
			Requests: []event.ToolRequest{bashCall(string(rune('a'+i)), "echo "+big)},
		})
	}
	r := newRig(t, replies, WithContextTokens(2000))
	r.runner.mu.Lock()
	r.runner.out = big + "\n"
	r.runner.mu.Unlock()

	r.run("first")
	var mark int
	r.eng.root.lock(func() { mark = r.eng.root.tr.mark() })
	r.run("second")

	require.NotEmpty(t, r.of(event.CompactedKind), "the budget must have forced a compaction")

	rebuilt := rebuild(t, r.of(event.AppendedKind))
	require.Zero(t, rebuilt.dropped, "a rebuild never compacts")

	rebuilt.truncate(mark)
	last := rebuilt.messages()[len(rebuilt.messages())-1]
	assert.Equal(t, "first", firstPrompt(rebuilt),
		"the rebuild still holds what the live session had dropped")
	assert.NotEmpty(t, last.Role, "and the mark landed on a real message")
}

// Restore is what a resume does to the engine: the transcript comes
// back and the next Turn carries on numbering.
func TestRestore_RebuildsTheTranscriptAndTheTurnCount(t *testing.T) {
	r := newRig(t, []model.Reply{
		{Text: "one", Requests: []event.ToolRequest{bashCall("c1", "ls")}},
	})
	r.run("first request")
	r.run("second request")

	live := r.eng.messages()
	records := asRecords(r.of(event.AppendedKind), r.of(event.TurnStartedKind))

	fresh := New(event.New(), &fakeModel{}, tool.Standard(), fakeSelector{&fakeRunner{}})
	defer fresh.unsub()
	fresh.Restore(records)

	assert.Equal(t, live, fresh.messages(), "the transcript comes back whole")
	assert.Equal(t, 2, fresh.turns, "and the next Turn is numbered 3")
}

// A crash leaves a Turn, its tool calls and the human's command open. Run ends
// them as facts, so the store records the end and a later resume sees it.
func TestRun_EndsWhatACrashLeftOpen(t *testing.T) {
	done, dead := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	step, call, cmd := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	records := asRecords([]event.Event{
		event.TurnStarted{Turn: done, N: 1, Prompt: "finished"},
		event.TurnEnded{Turn: done, Reason: event.EndDone},
		event.UserCommandStarted{UserCommand: cmd, Command: "tail -f log"},
		event.TurnStarted{Turn: dead, N: 2, Prompt: "build it"},
		event.Appended{Turn: dead, Messages: []event.Message{{Role: event.RoleUser, Content: "build it"}}},
		event.StepStarted{Turn: dead, Step: step, N: 1},
		event.ToolCallProposed{Step: step, ToolCall: call, Tool: "bash"},
		event.ToolCallStarted{ToolCall: call},
	})

	bus := event.New()
	eng := New(bus, &fakeModel{}, tool.Standard(), fakeSelector{&fakeRunner{}})
	eng.Restore(records)
	var mu sync.Mutex
	var got []event.Event
	unsub := bus.Handle(event.Facts(), func(rec event.Record) {
		mu.Lock()
		got = append(got, rec.Event)
		mu.Unlock()
	})
	ctx, cancel := context.WithCancel(context.Background())
	go eng.Run(ctx)
	t.Cleanup(func() { cancel(); unsub(); bus.Close() })
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.ContainsFunc(got, func(e event.Event) bool { return e.Kind() == event.MeasuredKind })
	}, 3*time.Second, 10*time.Millisecond)
	bus.Settle(time.Second)

	mu.Lock()
	defer mu.Unlock()
	var ended []event.Event
	for _, e := range got {
		switch e.(type) {
		case event.ToolCallEnded, event.UserCommandEnded, event.TurnEnded:
			ended = append(ended, e)
		}
	}
	require.Len(t, ended, 3, "the call, the command and the Turn, and not the finished Turn")
	assert.Equal(t, call, ended[0].(event.ToolCallEnded).ToolCall)
	assert.Equal(t, cmd, ended[1].(event.UserCommandEnded).UserCommand)
	assert.Equal(t, event.TurnEnded{Turn: dead, Reason: event.EndError,
		Summary: "detent exited before this request finished"}, ended[2])
	last := eng.messages()[len(eng.messages())-1]
	assert.Equal(t, cutOffNote, last.Content, "the model is told its request was cut off")
}

// Undo and reset take work back, so nothing they removed is ended again.
func TestLeftOpenBy_SkipsWhatWasTakenBack(t *testing.T) {
	kept, undone := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	step, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	open := leftOpenBy(asRecords([]event.Event{
		event.TurnStarted{Turn: kept},
		event.TurnStarted{Turn: undone},
		event.StepStarted{Turn: undone, Step: step},
		event.ToolCallProposed{Step: step, ToolCall: call},
		event.RolledBack{Turn: undone},
	}))
	assert.Equal(t, []uuid.UUID{kept}, open.turns)
	assert.Empty(t, open.calls, "the undone Turn's call went with it")

	open = leftOpenBy(asRecords([]event.Event{
		event.TurnStarted{Turn: kept},
		event.SessionReset{},
	}))
	assert.True(t, open.empty())
}

// asRecords puts facts back in publish order, the way a store hands
// them over.
func asRecords(groups ...[]event.Event) []event.Record {
	var out []event.Record
	var n uint64
	for _, g := range groups {
		for _, e := range g {
			n++
			out = append(out, event.Record{Ordinal: n, Event: e})
		}
	}
	return out
}

// rebuild is what a resume does: replay the appends in order, skipping
// Compacted, since a rebuild with no cut point is what resolves old marks.
func rebuild(t *testing.T, facts []event.Event) *transcript {
	t.Helper()
	tr := &transcript{}
	for _, f := range facts {
		if a, ok := f.(event.Appended); ok {
			tr.msgs = append(tr.msgs, a.Messages...)
		}
	}
	return tr
}

func firstPrompt(tr *transcript) string {
	for _, m := range tr.messages() {
		if m.Role == event.RoleUser {
			return m.Content
		}
	}
	return ""
}
