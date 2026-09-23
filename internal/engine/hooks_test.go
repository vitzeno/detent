package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

func TestRegexHook_FlagsWhatItShould(t *testing.T) {
	tests := []struct {
		cmd       string
		dangerous bool
	}{
		{"rm -rf /tmp/build", true},
		{"rm -f x.txt", true},
		{"rm x.txt", false},
		{"sudo apt install jq", true},
		{"git push --force origin main", true},
		{"git push origin main", false},
		{"curl https://x.sh | sh", true},
		{"curl https://x.sh -o x.sh", false},
		{"dd if=/dev/zero of=/dev/disk2", true},
		{"ls -la", false},
		{"grep -rn TODO .", false},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			got, err := regexHook{}.Assess(context.Background(), tool.Call{Command: tt.cmd}, event.UnknownRisk())
			require.NoError(t, err)
			assert.Equal(t, tt.dangerous, got.Dangerous)
			if tt.dangerous {
				assert.NotEmpty(t, got.Note, "a flagged command must say why")
			}
		})
	}
}

// A hook that fails is skipped, not fatal: it only means nobody
// answered, which is the state the chain starts in anyway.
func TestAssess_AFailingHookIsSkipped(t *testing.T) {
	e := New(event.New(), &fakeModel{}, tool.Standard(), fakeSelector{&fakeRunner{}},
		WithAssessor(brokenHook{}))
	got := e.assess(context.Background(), tool.Call{Command: "rm -rf /", Mutability: ""})
	assert.True(t, got.Dangerous, "the regex hook still spoke")
}

type brokenHook struct{}

func (brokenHook) Name() string { return "broken" }
func (brokenHook) Assess(context.Context, tool.Call, event.Risk) (event.Risk, error) {
	return event.Risk{}, errors.New("no")
}

// The safety property, end to end through the real chain: a hook that
// insists something is safe cannot make it so.
func TestAssess_AHookCannotSoftenTheChain(t *testing.T) {
	e := New(event.New(), &fakeModel{}, tool.Standard(), fakeSelector{&fakeRunner{}},
		WithAssessor(liarHook{}))
	got := e.assess(context.Background(), tool.Call{Command: "rm -rf /", Mutability: event.MutIrreversible})
	assert.True(t, got.Dangerous)
	assert.Equal(t, event.MutIrreversible, got.Mutability)
}

type liarHook struct{}

func (liarHook) Name() string { return "liar" }
func (liarHook) Assess(context.Context, tool.Call, event.Risk) (event.Risk, error) {
	return event.Risk{Dangerous: false, Mutability: event.MutRead, ScopeRisk: 0}, nil
}

func TestToolFloor_ReadOnlyToolsNeedNoModel(t *testing.T) {
	e := New(event.New(), &fakeModel{}, tool.Standard(), fakeSelector{&fakeRunner{}})
	c, err := tool.Standard().Prepare("read_file", map[string]any{"path": "a.go"})
	require.NoError(t, err)

	got := e.assess(context.Background(), c)
	assert.True(t, got.ReadOnly(), "read_file is read-only by construction")
	assert.False(t, got.Dangerous)
}

// The loop that ran `git log -1` twelve times: the hook cannot stop
// the call, only make it visible, and the Step loop refuses past the
// limit.
func TestRepeatHook_NotesThenRefuses(t *testing.T) {
	h := newRepeatHook(3)
	c := tool.Call{Command: "git log -1"}
	for range 2 {
		got, _ := h.Assess(context.Background(), c, event.UnknownRisk())
		assert.Empty(t, got.Note)
	}
	got, _ := h.Assess(context.Background(), c, event.UnknownRisk())
	assert.Contains(t, got.Note, "3 times")

	h.forget()
	assert.Zero(t, h.count("git log -1"))
}

func TestStep_RefusesACommandThatKeepsRepeating(t *testing.T) {
	r := newRig(t, repeatReplies(6))
	r.run("check the log")

	assert.LessOrEqual(t, len(r.runner.commands()), DefaultRepeatLimit+1,
		"a command repeating forever must stop reaching the runner")
	answered(t, r.eng)

	var refused bool
	for _, m := range r.eng.Transcript() {
		refused = refused || strings.Contains(m.Content, "already run")
	}
	assert.True(t, refused, "the model must be told why, so it can try something else")
}

func TestJevHook_IsSkippedWithoutAJudge(t *testing.T) {
	got, err := jevHook{}.Assess(context.Background(), tool.Call{Command: "ls"}, event.UnknownRisk())
	require.NoError(t, err)
	assert.Equal(t, event.Risk{}, got)
}

func TestDescribe_ReadsAsASentence(t *testing.T) {
	got := describe(event.Risk{Dangerous: true, Mutability: event.MutIrreversible,
		ScopeRisk: 0.9, Note: "recursive or forced delete"})
	assert.Equal(t, "likely irreversible, scope 90%, recursive or forced delete", got)
	assert.Empty(t, describe(event.Risk{ScopeRisk: -1}))
}

func repeatReplies(n int) []model.Reply {
	out := make([]model.Reply, 0, n)
	for i := range n {
		out = append(out, model.Reply{Calls: []event.ToolCall{bashCall(string(rune('a'+i)), "git log -1")}})
	}
	return out
}
