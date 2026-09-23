package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/model"
)

// wellFormed is the invariant the whole rewrite rests on: every
// tool_call id has exactly one answer, and no tool message answers a
// call that was never made.
func wellFormed(t *testing.T, msgs []model.Message) {
	t.Helper()
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		if m.Role == model.RoleTool {
			t.Fatalf("message %d is a tool result with no assistant before it", i)
		}
		if m.Role != model.RoleAssistant || len(m.Calls) == 0 {
			continue
		}
		want := model.IDs(m.Calls)
		var got []string
		for j := i + 1; j < len(msgs) && msgs[j].Role == model.RoleTool; j++ {
			got = append(got, msgs[j].CallID)
			i = j
		}
		assert.Equal(t, want, got, "step at %d: every call needs one answer, in order", i)
	}
}

func call(id, name string) model.ToolCall {
	return model.ToolCall{ID: id, Name: name, Args: map[string]any{}}
}

// The gate: whatever goes wrong in a Step, the transcript that comes
// out must still be one the next Step can be built on.
func TestStep_EveryCallIsAnsweredHoweverItWent(t *testing.T) {
	calls := []model.ToolCall{call("c1", "bash"), call("c2", "read_file"), call("c3", "write_file")}
	reply := model.Reply{Text: "working", Calls: calls}

	tests := []struct {
		name    string
		answers map[string]string
		want    []string // what each call's result must contain
	}{
		{
			name:    "all ran",
			answers: map[string]string{"c1": "ok", "c2": "contents", "c3": "written"},
			want:    []string{"ok", "contents", "written"},
		},
		{
			name:    "one declined, siblings unaffected",
			answers: map[string]string{"c1": "ok", "c2": "the human declined this", "c3": "written"},
			want:    []string{"ok", "declined", "written"},
		},
		{
			name:    "aborted partway",
			answers: map[string]string{"c1": "ok", "c2": "aborted", "c3": "aborted"},
			want:    []string{"ok", "aborted", "aborted"},
		},
		{
			name:    "a bad tool name never reached the runner",
			answers: map[string]string{"c1": `no tool named "bash"`, "c2": "contents", "c3": "written"},
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
			tr.user("do it")
			tr.step(reply, tt.answers)

			wellFormed(t, tr.messages())
			msgs := tr.messages()
			require.Len(t, msgs, 5, "user, assistant, three answers")
			for i, want := range tt.want {
				assert.Contains(t, msgs[2+i].Content, want)
				assert.Equal(t, calls[i].ID, msgs[2+i].CallID)
			}
		})
	}
}

func TestStep_BoundsAHugeResult(t *testing.T) {
	var tr transcript
	tr.user("go")
	tr.step(model.Reply{Calls: []model.ToolCall{call("c1", "bash")}},
		map[string]string{"c1": strings.Repeat("x", MaxResultBytes*3)})

	got := tr.messages()[2].Content
	assert.Less(t, len(got), MaxResultBytes+64, "one result cannot eat the whole budget")
	assert.Contains(t, got, "truncated")
}

// Compaction moves whole Steps. Half a Step is a transcript no
// endpoint accepts, so this is the property, not the byte count.
func TestCompact_MovesWholeStepsOnly(t *testing.T) {
	var tr transcript
	big := strings.Repeat("x", 2000)
	for turn := range 8 {
		tr.user("request " + string(rune('a'+turn)))
		for step := range 3 {
			c := call(string(rune('a'+turn))+string(rune('0'+step)), "bash")
			tr.step(model.Reply{Text: big, Calls: []model.ToolCall{c}},
				map[string]string{c.ID: big})
		}
	}
	before := tr.bytes()
	require.True(t, tr.compact(context.Background(), 4, nil), "should have been over a 4-token budget")

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
		tr.user("old request")
		c := call("old", "bash")
		tr.step(model.Reply{Text: big, Calls: []model.ToolCall{c}}, map[string]string{c.ID: big})
	}
	tr.user("THE OPEN REQUEST")
	c := call("live", "bash")
	tr.step(model.Reply{Text: big, Calls: []model.ToolCall{c}}, map[string]string{c.ID: big})

	tr.compact(context.Background(), 1, nil) // impossible budget

	var text string
	for _, m := range tr.messages() {
		text += m.Content + "\n"
	}
	assert.Contains(t, text, "THE OPEN REQUEST")
	wellFormed(t, tr.messages()[1:])
}

type stubSummarizer struct{ out string }

func (s stubSummarizer) Summarize(context.Context, []model.Message) (string, error) {
	return s.out, nil
}

func TestCompact_UsesASummarizerWhenWired(t *testing.T) {
	var tr transcript
	big := strings.Repeat("x", 3000)
	for range 6 {
		tr.user("old")
		c := call("c", "bash")
		tr.step(model.Reply{Text: big, Calls: []model.ToolCall{c}}, map[string]string{c.ID: big})
	}
	tr.compact(context.Background(), 2, stubSummarizer{out: "cloned the repo, tests pass"})
	assert.Contains(t, tr.messages()[0].Content, "cloned the repo, tests pass")
}

func TestCompact_NoOpWhenUnderBudget(t *testing.T) {
	var tr transcript
	tr.user("small")
	assert.False(t, tr.compact(context.Background(), DefaultContextTokens, nil))
	assert.Len(t, tr.messages(), 1)
}

// Rollback rewinds to a Turn boundary, which is a whole number of
// Steps by construction.
func TestTruncate_LeavesAWellFormedTranscript(t *testing.T) {
	var tr transcript
	tr.user("first")
	c1 := call("c1", "bash")
	tr.step(model.Reply{Calls: []model.ToolCall{c1}}, map[string]string{c1.ID: "ok"})

	at := tr.mark()
	tr.user("second")
	c2 := call("c2", "bash")
	tr.step(model.Reply{Calls: []model.ToolCall{c2}}, map[string]string{c2.ID: "ok"})

	tr.truncate(at)
	wellFormed(t, tr.messages())
	assert.Len(t, tr.messages(), 3)
	assert.Equal(t, "first", tr.messages()[0].Content)
}

func TestUnitEnd_GroupsAnAssistantWithItsAnswers(t *testing.T) {
	msgs := []model.Message{
		{Role: model.RoleUser},
		{Role: model.RoleAssistant, Calls: []model.ToolCall{call("a", "x"), call("b", "y")}},
		{Role: model.RoleTool, CallID: "a"},
		{Role: model.RoleTool, CallID: "b"},
		{Role: model.RoleAssistant},
	}
	assert.Equal(t, 1, unitEnd(msgs, 0), "a user message is its own unit")
	assert.Equal(t, 4, unitEnd(msgs, 1), "an assistant owns its answers")
	assert.Equal(t, 5, unitEnd(msgs, 4), "prose with no calls stands alone")
	assert.Equal(t, 5, unitEnd(msgs, 9), "past the end is the end")
}

// A mark names a Turn a human may still undo, and compaction rewrites
// the front underneath it. Slice positions moved; appends do not.
func TestMark_SurvivesCompaction(t *testing.T) {
	var tr transcript
	big := strings.Repeat("x", 3000)

	tr.user("first request")
	c1 := call("c1", "bash")
	tr.step(model.Reply{Text: big, Calls: []model.ToolCall{c1}}, map[string]string{c1.ID: big})

	// The Turn a human would undo.
	target := tr.mark()
	tr.user("second request")
	c2 := call("c2", "bash")
	tr.step(model.Reply{Text: big, Calls: []model.ToolCall{c2}}, map[string]string{c2.ID: big})

	before := len(tr.messages())
	require.True(t, tr.compact(context.Background(), 2, nil), "should have compacted")
	require.Less(t, len(tr.messages()), before, "the front moved")

	tr.truncate(target)
	wellFormed(t, tr.messages())

	var text string
	for _, m := range tr.messages() {
		text += m.Content + "\n"
	}
	assert.NotContains(t, text, "second request", "the undone Turn must be gone")
}

// A mark compaction has eaten is a no-op, not a truncation to the
// wrong place.
func TestTruncate_IgnoresAMarkCompactionAteAsWellAsOneTooLarge(t *testing.T) {
	var tr transcript
	big := strings.Repeat("x", 4000)
	for range 6 {
		tr.user("old")
		c := call("c", "bash")
		tr.step(model.Reply{Text: big, Calls: []model.ToolCall{c}}, map[string]string{c.ID: big})
	}
	stale := 1 // a mark from the very start
	tr.compact(context.Background(), 1, nil)

	n := len(tr.messages())
	tr.truncate(stale)
	assert.Len(t, tr.messages(), n, "a mark that no longer exists changes nothing")

	tr.truncate(tr.mark() + 100)
	assert.Len(t, tr.messages(), n, "and neither does one past the end")
}
