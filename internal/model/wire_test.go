package model

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/usercommand"
)

// A Step on the wire: one assistant message carrying tool_calls, then
// one tool message per id. Endpoints reject either half alone.
func TestEncode_AStepIsAnAssistantPlusItsAnswers(t *testing.T) {
	call := event.ToolRequest{ID: "c1", Name: "read_file", Args: map[string]any{"path": "a.go"}}
	step := []event.Message{
		{Role: event.RoleUser, Content: "read a.go"},
		{Role: event.RoleAssistant, Content: "reading", Requests: []event.ToolRequest{call}},
		event.Answer(call, "package main"),
	}

	c, got := serve(t, `{"choices":[{"message":{"content":"done"}}]}`)
	_, _, err := c.Complete(context.Background(), step, nil)
	require.NoError(t, err)

	msgs := (*got)["messages"].([]any)
	require.Len(t, msgs, 4, "the system prompt plus the three given")

	assistant := msgs[2].(map[string]any)
	assert.Equal(t, "assistant", assistant["role"])
	calls := assistant["tool_calls"].([]any)
	require.Len(t, calls, 1)
	fn := calls[0].(map[string]any)
	assert.Equal(t, "c1", fn["id"])
	assert.Equal(t, "function", fn["type"])
	args, ok := fn["function"].(map[string]any)["arguments"].(string)
	require.True(t, ok, "arguments is a JSON string, not an object")
	assert.JSONEq(t, `{"path":"a.go"}`, args)

	answer := msgs[3].(map[string]any)
	assert.Equal(t, "tool", answer["role"])
	assert.Equal(t, "c1", answer["tool_call_id"], "the id is what pairs it to the call")
	assert.Equal(t, "package main", answer["content"])
}

// Only a tool message carries tool_call_id, and only an assistant one
// tool_calls. A stray field makes endpoints reject the whole transcript.
func TestEncode_FieldsStayOnTheirOwnRole(t *testing.T) {
	c, got := serve(t, `{"choices":[{"message":{"content":"ok"}}]}`)
	_, _, err := c.Complete(context.Background(), []event.Message{
		{Role: event.RoleUser, Content: "hi"},
		{Role: event.RoleAssistant, Content: "prose, no calls"},
	}, nil)
	require.NoError(t, err)

	for _, m := range (*got)["messages"].([]any) {
		msg := m.(map[string]any)
		assert.NotContains(t, msg, "tool_calls", "%v", msg["role"])
		assert.NotContains(t, msg, "tool_call_id", "%v", msg["role"])
	}
}

func TestComplete_SendsToolsAndTheSystemPrompt(t *testing.T) {
	schemas := []map[string]any{{"type": "function", "function": map[string]any{"name": "bash"}}}
	c, got := serve(t, `{"choices":[{"message":{"content":"ok"}}]}`)
	c.Env = Environment{OS: "linux", Arch: "arm64", Dir: "/workspace", Sandboxed: true, Undoable: true}

	_, _, err := c.Complete(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}}, schemas)
	require.NoError(t, err)

	assert.Len(t, (*got)["tools"], 1)
	sys := (*got)["messages"].([]any)[0].(map[string]any)
	assert.Equal(t, "system", sys["role"])

	text := sys["content"].(string)
	assert.Contains(t, text, "linux/arm64", "the prompt must describe where commands run")
	assert.Contains(t, text, "/workspace")
	assert.Contains(t, text, "container")
	assert.Contains(t, text, "undo the whole request", "Undoable is per Turn")
}

// The project's instructions come after the built-in rules, so those stay first.
func TestSystemPrompt_EndsWithTheProjectsInstructions(t *testing.T) {
	c := &Client{Env: LocalEnvironment(), Instructions: "<instructions path=\"AGENTS.md\">\nuse tabs\n</instructions>"}
	got := c.systemPrompt()
	assert.True(t, strings.HasSuffix(got, "use tabs\n</instructions>"))
	assert.Less(t, strings.Index(got, "Work the request"), strings.Index(got, "use tabs"))
	assert.Equal(t, systemPrompt(LocalEnvironment()), (&Client{Env: LocalEnvironment()}).systemPrompt(),
		"no instructions adds nothing")
}

// /context labels the prompt from these parts, so a piece added to the
// prompt but not listed here would be counted under nothing.
func TestPromptParts_AddUpToTheWholePrompt(t *testing.T) {
	for name, c := range map[string]*Client{
		"bare":              {Env: LocalEnvironment()},
		"with instructions": {Env: LocalEnvironment(), Instructions: "<instructions>x</instructions>", InstructionFiles: []string{"AGENTS.md"}},
		"set by the caller": {SystemPrompt: "you are a test"},
	} {
		n := 0
		for _, p := range c.PromptParts() {
			n += p.Bytes
		}
		assert.Equal(t, len(c.systemPrompt()), n, name)
	}
}

func TestBrief_WritesADurationAsAPersonWould(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Minute: "10m", 90 * time.Second: "1m30s", time.Hour: "1h",
		90 * time.Minute: "1h30m", 50 * time.Millisecond: "50ms",
	} {
		assert.Equal(t, want, Brief(d))
	}
}

func TestSystemPrompt_StatesTheCommandLimit(t *testing.T) {
	env := LocalEnvironment()
	assert.NotContains(t, systemPrompt(env), "is stopped")
	env.Timeout = 10 * time.Minute
	assert.Contains(t, systemPrompt(env), "A command still running after 10m is stopped.")
}

// The prompt promises a shape another package emits, so it is checked
// against that package's constant: prose drifting from it fails silently.
func TestSystemPrompt_NamesTheMarkerHumanshellActuallyWrites(t *testing.T) {
	assert.Contains(t, systemPrompt(LocalEnvironment()), usercommand.Marker)
	assert.Contains(t, systemPrompt(LocalEnvironment()), "read it rather than running it again",
		"and says what to do with it, which is the point of naming it")
}

// Point 4 says earlier steps are still on disk, which a resume makes
// false, so its exception must name the marker the note opens with.
func TestSystemPrompt_NamesTheResumeMarker(t *testing.T) {
	got := systemPrompt(LocalEnvironment())
	assert.Contains(t, got, ResumeMarker)
	assert.Contains(t, got, "is the exception, and says what survived")
}

// Without tools the request must not carry an empty array: some
// endpoints treat its presence as a demand to call something.
func TestComplete_OmitsToolsWhenThereAreNone(t *testing.T) {
	c, got := serve(t, `{"choices":[{"message":{"content":"ok"}}]}`)
	_, _, err := c.Complete(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}}, nil)
	require.NoError(t, err)
	assert.NotContains(t, *got, "tools")
}

func TestEnvironment_SaysWhatChanges(t *testing.T) {
	tests := []struct {
		name  string
		env   Environment
		wants []string
		nots  []string
	}{
		{
			name:  "unsandboxed says the files are real",
			env:   Environment{OS: "darwin", Arch: "arm64", Dir: "/x", Network: true},
			wants: []string{"own machine", "network is reachable"},
			nots:  []string{"container", "undo"},
		},
		{
			name:  "no network says so plainly",
			env:   Environment{OS: "linux", Arch: "amd64", Dir: "/x"},
			wants: []string{"no network", "Work with what is already here"},
			nots:  []string{"network is reachable"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := systemPrompt(tt.env)
			for _, w := range tt.wants {
				assert.Contains(t, got, w)
			}
			for _, n := range tt.nots {
				assert.NotContains(t, got, n)
			}
		})
	}
}

func TestLocalEnvironment_DescribesThisMachine(t *testing.T) {
	env := LocalEnvironment()
	assert.NotEmpty(t, env.OS)
	assert.NotEmpty(t, env.Dir)
	assert.True(t, env.Network)
	assert.False(t, env.Undoable, "nothing checkpoints the user's own filesystem")
	assert.False(t, env.Sandboxed)
}
