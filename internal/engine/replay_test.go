package engine

import (
	"strings"
	"testing"

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
		{Text: "looking", Calls: []event.ToolCall{
			bashCall("c1", "ls"), readCall("c2", "a.go")}},
		{Text: "and one more", Calls: []event.ToolCall{bashCall("c3", "pwd")}},
	})
	r.run("do the thing")

	live := r.eng.Transcript()
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
			Text:  big,
			Calls: []event.ToolCall{bashCall(string(rune('a'+i)), "echo "+big)},
		})
	}
	r := newRig(t, replies, WithContextTokens(2000))
	r.runner.mu.Lock()
	r.runner.out = big + "\n"
	r.runner.mu.Unlock()

	r.run("first")
	var mark int
	r.eng.trLock(func() { mark = r.eng.tr.mark() })
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
		{Text: "one", Calls: []event.ToolCall{bashCall("c1", "ls")}},
	})
	r.run("first request")
	r.run("second request")

	live := r.eng.Transcript()
	records := asRecords(r.of(event.AppendedKind), r.of(event.TurnStartedKind))

	fresh := New(event.New(), &fakeModel{}, tool.Standard(), fakeSelector{&fakeRunner{}})
	defer fresh.unsub()
	fresh.Restore(records)

	assert.Equal(t, live, fresh.Transcript(), "the transcript comes back whole")
	assert.Equal(t, 2, fresh.turns, "and the next Turn is numbered 3")
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
