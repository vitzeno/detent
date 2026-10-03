package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/model"
)

// The gate: whatever goes wrong in a Step, the transcript that comes
// out must still be one the next Step can be built on.
func TestStep_EveryToolCallIsAnsweredHoweverItWent(t *testing.T) {
	calls := []event.ToolRequest{call("c1", "bash"), call("c2", "read_file"), call("c3", "write_file")}
	reply := model.Reply{Text: "working", Requests: calls}

	tests := []struct {
		name    string
		answers []string
		want    []string // what each call's result must contain
	}{
		{
			name:    "all ran",
			answers: []string{"ok", "contents", "written"},
			want:    []string{"ok", "contents", "written"},
		},
		{
			name:    "one declined, siblings unaffected",
			answers: []string{"ok", "the human declined this", "written"},
			want:    []string{"ok", "declined", "written"},
		},
		{
			name:    "aborted partway",
			answers: []string{"ok", "aborted", "aborted"},
			want:    []string{"ok", "aborted", "aborted"},
		},
		{
			name:    "a bad tool name never reached the runner",
			answers: []string{`no tool named "bash"`, "contents", "written"},
			want:    []string{"no tool named", "contents", "written"},
		},
		{
			name:    "nothing answered at all",
			answers: nil,
			want:    []string{"did not run", "did not run", "did not run"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tr transcript
			tr.user(0, "do it")
			tr.step(reply, tt.answers)

			wellFormed(t, tr.messages())
			msgs := tr.messages()
			require.Len(t, msgs, 5, "user, assistant, three answers")
			for i, want := range tt.want {
				assert.Contains(t, msgs[2+i].Content, want)
				assert.Equal(t, calls[i].ID, msgs[2+i].RequestID)
			}
		})
	}
}

func TestStep_BoundsAHugeResult(t *testing.T) {
	var tr transcript
	tr.user(0, "go")
	tr.step(model.Reply{Requests: []event.ToolRequest{call("c1", "bash")}},
		[]string{strings.Repeat("x", maxResultBytes*3)})

	got := tr.messages()[2].Content
	assert.Less(t, len(got), maxResultBytes+64, "one result cannot eat the whole budget")
	assert.Contains(t, got, "truncated")
}

// Compaction moves whole Steps. Half a Step is a transcript no
// endpoint accepts, so this is the property, not the byte count.
func TestCompact_MovesWholeStepsOnly(t *testing.T) {
	var tr transcript
	big := strings.Repeat("x", 2000)
	for turn := range 8 {
		tr.user(0, "request "+string(rune('a'+turn)))
		for step := range 3 {
			c := call(string(rune('a'+turn))+string(rune('0'+step)), "bash")
			tr.step(model.Reply{Text: big, Requests: []event.ToolRequest{c}},
				[]string{big})
		}
	}
	before := tr.bytes()
	dropped, _ := tr.compact(context.Background(), 4, nil)
	require.Positive(t, dropped, "should have been over a 4-token budget")

	assert.Less(t, tr.bytes(), before)
	wellFormed(t, tr.messages()[1:]) // [0] is the dropped-messages note
	assert.Contains(t, tr.messages()[0].Content, "dropped")
}

// Nothing from the open Turn may go: the model cannot work a request
// it can no longer see.
func TestCompact_NeverDropsTheOpenTurn(t *testing.T) {
	var tr transcript
	big := strings.Repeat("x", 5000)
	for range 5 {
		tr.user(0, "old request")
		c := call("old", "bash")
		tr.step(model.Reply{Text: big, Requests: []event.ToolRequest{c}}, []string{big})
	}
	tr.user(0, "THE OPEN REQUEST")
	c := call("live", "bash")
	tr.step(model.Reply{Text: big, Requests: []event.ToolRequest{c}}, []string{big})

	tr.compact(context.Background(), 1, nil) // impossible budget

	var text strings.Builder
	for _, m := range tr.messages() {
		text.WriteString(m.Content + "\n")
	}
	assert.Contains(t, text.String(), "THE OPEN REQUEST")
	wellFormed(t, tr.messages()[1:])
}

func TestCompact_UsesASummarizerWhenWired(t *testing.T) {
	var tr transcript
	big := strings.Repeat("x", 3000)
	for range 6 {
		tr.user(0, "old")
		c := call("c", "bash")
		tr.step(model.Reply{Text: big, Requests: []event.ToolRequest{c}}, []string{big})
	}
	_, _ = tr.compact(context.Background(), 2, stubSummarizer{out: "cloned the repo, tests pass"})
	assert.Contains(t, tr.messages()[0].Content, "cloned the repo, tests pass")
}

func TestCompact_NoOpWhenUnderBudget(t *testing.T) {
	var tr transcript
	tr.user(0, "small")
	dropped, _ := tr.compact(context.Background(), DefaultContextTokens, nil)
	assert.Zero(t, dropped)
	assert.Len(t, tr.messages(), 1)
}

// Rollback rewinds to a Turn boundary, which is a whole number of
// Steps by construction.
func TestTruncate_LeavesAWellFormedTranscript(t *testing.T) {
	var tr transcript
	tr.user(0, "first")
	c1 := call("c1", "bash")
	tr.step(model.Reply{Requests: []event.ToolRequest{c1}}, []string{"ok"})

	at := tr.mark()
	tr.user(0, "second")
	c2 := call("c2", "bash")
	tr.step(model.Reply{Requests: []event.ToolRequest{c2}}, []string{"ok"})

	tr.truncate(at)
	wellFormed(t, tr.messages())
	assert.Len(t, tr.messages(), 3)
	assert.Equal(t, "first", tr.messages()[0].Content)
}

func TestUnitEnd_GroupsAnAssistantWithItsAnswers(t *testing.T) {
	msgs := []event.Message{
		{Role: event.RoleUser},
		{Role: event.RoleAssistant, Requests: []event.ToolRequest{call("a", "x"), call("b", "y")}},
		{Role: event.RoleTool, RequestID: "a"},
		{Role: event.RoleTool, RequestID: "b"},
		{Role: event.RoleAssistant},
	}
	assert.Equal(t, 1, unitEnd(msgs, 0), "a user message is its own unit")
	assert.Equal(t, 4, unitEnd(msgs, 1), "an assistant owns its answers")
	assert.Equal(t, 5, unitEnd(msgs, 4), "prose with no calls stands alone")
	assert.Equal(t, 5, unitEnd(msgs, 9), "past the end is the end")
}

// A mark names a Turn a human may still undo, and compaction rewrites
// the front underneath it. Slice positions move, appends do not.
func TestMark_SurvivesCompaction(t *testing.T) {
	var tr transcript
	big := strings.Repeat("x", 3000)

	tr.user(0, "first request")
	c1 := call("c1", "bash")
	tr.step(model.Reply{Text: big, Requests: []event.ToolRequest{c1}}, []string{big})

	// The Turn a human would undo.
	target := tr.mark()
	tr.user(0, "second request")
	c2 := call("c2", "bash")
	tr.step(model.Reply{Text: big, Requests: []event.ToolRequest{c2}}, []string{big})

	before := len(tr.messages())
	dropped, _ := tr.compact(context.Background(), 2, nil)
	require.Positive(t, dropped, "should have compacted")
	require.Less(t, len(tr.messages()), before, "the front moved")

	tr.truncate(target)
	wellFormed(t, tr.messages())

	var text strings.Builder
	for _, m := range tr.messages() {
		text.WriteString(m.Content + "\n")
	}
	assert.NotContains(t, text.String(), "second request", "the undone Turn must be gone")
}

// A mark compaction has eaten is a no-op, not a truncation to the
// wrong place.
func TestTruncate_IgnoresAMarkCompactionAteAsWellAsOneTooLarge(t *testing.T) {
	var tr transcript
	big := strings.Repeat("x", 4000)
	for range 6 {
		tr.user(0, "old")
		c := call("c", "bash")
		tr.step(model.Reply{Text: big, Requests: []event.ToolRequest{c}}, []string{big})
	}
	stale := 1 // a mark from the very start
	_, _ = tr.compact(context.Background(), 1, nil)

	n := len(tr.messages())
	tr.truncate(stale)
	assert.Len(t, tr.messages(), n, "a mark that no longer exists changes nothing")

	tr.truncate(tr.mark() + 100)
	assert.Len(t, tr.messages(), n, "and neither does one past the end")
}

// A mark is only valid against the compaction it was taken under, so a
// rebuild that never compacts is what resolves old marks on resume.
func TestReplay_UncompactedRebuildResolvesOldMarks(t *testing.T) {
	big := strings.Repeat("x", 2000)

	build := func() (*transcript, int) {
		tr := &transcript{}
		var mark int
		for i := range 8 {
			if i == 4 {
				mark = tr.mark()
			}
			tr.user(0, "request")
			c := call(string(rune('a'+i)), "bash")
			tr.step(model.Reply{Text: big, Requests: []event.ToolRequest{c}},
				[]string{big})
		}
		return tr, mark
	}

	orig, mark := build()
	dropped, _ := orig.compact(context.Background(), 6000, nil)
	require.Positive(t, dropped)
	orig.truncate(mark)

	// The replay: same appends, never compacted.
	replayed, replayMark := build()
	require.Equal(t, mark, replayMark)
	require.Zero(t, replayed.dropped)
	replayed.truncate(replayMark)

	assert.Equal(t, lastAppend(orig), lastAppend(replayed),
		"an uncompacted rebuild must end at the same append")
}

// An open Turn over budget leaves only a sliver droppable, which must
// not cost a summariser round trip every Step.
func TestCompact_WillNotPayASummarizerForASliver(t *testing.T) {
	const budgetTokens = 1000
	budget := budgetTokens * bytesPerToken

	var tr transcript
	tr.user(0, strings.Repeat("o", budget/50)) // the droppable sliver
	c := call("old", "bash")
	tr.step(model.Reply{Requests: []event.ToolRequest{c}}, []string{"x"})

	tr.user(0, strings.Repeat("L", 2*budget)) // an open Turn over budget on its own

	s := &countingSummarizer{}
	for range 5 {
		dropped, _ := tr.compact(context.Background(), budgetTokens, s)
		assert.Zero(t, dropped)
	}
	assert.Zero(t, s.calls, "a sliver that cannot reach budget is not worth a round trip")
}

// The guard must not stop compaction that actually works.
func TestCompact_StillCutsWhenTheCutReachesBudget(t *testing.T) {
	const budgetTokens = 1000
	budget := budgetTokens * bytesPerToken

	var tr transcript
	for range 4 {
		tr.user(0, strings.Repeat("o", budget/2))
	}
	tr.user(0, "the open request")

	s := &countingSummarizer{}
	dropped, _ := tr.compact(context.Background(), budgetTokens, s)
	assert.NotZero(t, dropped)
	assert.Equal(t, 1, s.calls)
	assert.LessOrEqual(t, tr.bytes(), budget)
}

// Compaction stalls the Turn for seconds. Saying so first is the
// difference between a wait and a hang.
func TestCompact_SaysSoBeforeItStalls(t *testing.T) {
	big := strings.Repeat("x", 4000)
	replies := []model.Reply{
		{Requests: []event.ToolRequest{bashCall("a", "one")}},
		{Requests: []event.ToolRequest{bashCall("b", "two")}},
	}
	r := rigWith(t, event.New(), &fakeModel{replies: replies}, &fakeRunner{out: big + "\n"},
		WithContextTokens(200), WithSummarizer(stubSummarizer{out: "earlier work"}))
	// Two Turns: nothing inside the open one is droppable, so the
	// first has to close before there is anything to compact.
	r.run("go")
	r.run("again")

	require.NotEmpty(t, r.of(event.CompactedKind), "the test needs compaction to have run")
	var said bool
	for _, e := range r.of(event.NoticeKind) {
		if strings.Contains(e.(event.Notice).Text, "compact") {
			said = true
		}
	}
	assert.True(t, said, "compaction stalled the Turn without saying so")
}

// A Turn under budget must not flash a compaction that never happens.
func TestCompact_SaysNothingWhenItDoesNotRun(t *testing.T) {
	r := newRig(t, []model.Reply{{Requests: []event.ToolRequest{bashCall("a", "ls")}}})
	r.run("go")

	require.Empty(t, r.of(event.CompactedKind), "nothing should have compacted")
	for _, e := range r.of(event.NoticeKind) {
		assert.NotContains(t, e.(event.Notice).Text, "compact",
			"announced a compaction that never ran")
	}
}

// wellFormed checks the invariant the transcript rests on: every
// tool_call id has one answer, and no tool message answers nothing.
func wellFormed(t *testing.T, msgs []event.Message) {
	t.Helper()
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		if m.Role == event.RoleTool {
			t.Fatalf("message %d is a tool result with no assistant before it", i)
		}
		if m.Role != event.RoleAssistant || len(m.Requests) == 0 {
			continue
		}
		want := event.RequestIDs(m.Requests)
		var got []string
		for j := i + 1; j < len(msgs) && msgs[j].Role == event.RoleTool; j++ {
			got = append(got, msgs[j].RequestID)
			i = j
		}
		assert.Equal(t, want, got, "step at %d: every call needs one answer, in order", i)
	}
}

func call(id, name string) event.ToolRequest {
	return event.ToolRequest{ID: id, Name: name, Args: map[string]any{}}
}

type stubSummarizer struct{ out string }

func (s stubSummarizer) Summarize(context.Context, []event.Message) (string, error) {
	return s.out, nil
}

func lastAppend(tr *transcript) string {
	m := tr.messages()
	if len(m) == 0 {
		return ""
	}
	return string(m[len(m)-1].Role) + ":" + m[len(m)-1].RequestID
}

type countingSummarizer struct{ calls int }

func (c *countingSummarizer) Summarize(context.Context, []event.Message) (string, error) {
	c.calls++
	return "summary", nil
}
