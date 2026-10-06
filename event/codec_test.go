package event

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

// A type with a Kind method but no codec fails only on replay, so read the
// source for the real list rather than keeping one by hand.
func TestCodecs_CoverEveryEventType(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	declared := map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "Kind" || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			id, ok := fn.Recv.List[0].Type.(*ast.Ident)
			require.True(t, ok, "%s: Kind must have a value receiver, or codecs cannot decode it", name)
			declared[id.Name] = true
		}
	}
	require.NotEmpty(t, declared, "found no event types at all")

	registered := map[string]bool{}
	for k := range codecs {
		e, err := Decode(k, []byte(`{}`))
		require.NoError(t, err)
		registered[typeName(e)] = true
	}

	for name := range declared {
		assert.True(t, registered[name],
			"%s has a Kind method but no codec, so it would not survive a replay", name)
	}
	assert.Len(t, codecs, len(declared), "a codec is registered for a type that no longer exists")
}

// Every field has to survive the trip, because a store writes the
// payload once and reads it back much later.
func TestCodec_RoundTripsEveryField(t *testing.T) {
	turn, step, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	shell := uuid.Must(uuid.NewV7())
	spec := &viewspec.Spec{Blocks: []viewspec.Block{{Kind: "log"}}}

	cases := []Event{
		SessionStarted{Session: turn, Model: "m", Sandbox: true, Network: true, MaxSteps: 50, Instructions: []string{"AGENTS.md"},
			Subagents: true, MaxAgents: 10, Commit: "98d2dd2"},
		SessionResumed{Session: turn, Records: 412, Sandbox: true},
		TurnStarted{Turn: turn, N: 3, Prompt: "do it"},
		TurnEnded{Turn: turn, Reason: EndDone, Summary: "did it",
			Usage: Usage{PromptTokens: 7, CompletionTokens: 2, Latency: 90 * time.Millisecond, Model: "m"}},
		CheckpointTaken{Turn: turn, Snapshot: "snap", Tree: "tree"},
		BoundReached{Turn: turn, Steps: 50, ToolCalls: 9},
		RolledBack{Turn: turn, RevertFiles: true},
		SessionReset{},
		StepStarted{Turn: turn, Step: step, N: 2, Agent: agent},
		AgentStarted{Agent: agent, ToolCall: call, Name: "explore-auth", Task: "find the session code"},
		AgentEnded{Agent: agent, Reason: AgentPartial, Usage: Usage{PromptTokens: 7}},
		StepEnded{Turn: turn, Step: step, ToolCalls: 3, Usage: Usage{PromptTokens: 1}},
		ModelText{Turn: turn, Step: step, Text: "prose"},
		Appended{Turn: turn, Step: step, Messages: []Message{
			{Role: RoleAssistant, Content: "looking", Requests: []ToolRequest{
				{ID: "c1", Name: "bash", Args: map[string]any{"command": "ls"}, Err: "boom"}}},
			{Role: RoleTool, RequestID: "c1", Content: "out"},
		}},
		Compacted{Turn: turn, Dropped: 12, Note: "summary"},
		ContextMeasured{Budget: 200, Total: 41, Exact: true, Growth: 2,
			Fixed:   []ContextPart{{Name: "tools", Detail: "9 built-in", Tokens: 3}},
			History: []ContextPart{{Name: "fix it", Tokens: 9, N: 4, Open: true}}},
		ToolCallProposed{ToolCall: call, Step: step, Tool: "bash", Args: map[string]any{"command": "ls"},
			Rationale: "why", Renders: RendersMarkdown, Executor: "github"},
		ToolCallAssessed{ToolCall: call, Risk: Risk{Dangerous: true, Mutability: MutSystem,
			ScopeRisk: 0.8, Note: "n", FromJudge: true}},
		ApprovalAsked{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "rm"},
			Rationale: "r", Risk: Risk{Dangerous: true}},
		ToolCallStarted{ToolCall: call, Runner: "sandbox"},
		OutputChunk{ToolCall: call, Line: "a line", Stderr: true},
		ToolCallEnded{ToolCall: call, Took: 21 * time.Millisecond, Result: Result{
			ExitCode: 2, Stdout: "o", Stderr: "e", Truncated: true, Err: "x"}},
		ToolCallJudged{ToolCall: call, Status: "failed", RenderKind: "errors",
			Attention: 0.9, GoalAchieved: 0.1, FromJudge: true},
		ViewReady{ToolCall: call, Spec: spec, Source: "composed"},
		UserCommandStarted{UserCommand: shell, Command: "git status", Runner: "sandbox"},
		UserCommandEnded{UserCommand: shell, Took: 8 * time.Millisecond, Result: Result{
			ExitCode: 1, Stdout: "o", Stderr: "e", Truncated: true, Err: "x"}},
		Notice{Level: "warn", Text: "careful"},
		SessionLoaded{Session: turn, Err: "no such session"},
		SessionsListed{Sessions: []SessionSummary{{ID: turn, Name: "named", Started: time.UnixMilli(1_700_000_000_000).UTC(), Model: "m", Events: 12}}},
		ServersListed{Servers: []ServerSummary{
			{Name: "github", Command: "docker", Tools: 12},
			{Name: "broken", Command: "nope", Err: "no such file", Disabled: true},
			{Name: "notion", Command: "https://mcp.notion.com/mcp", Auth: AuthWaiting},
		}},
		AuthorizationWaiting{Server: "notion", URL: "https://mcp.notion.com/authorize?state=s",
			Until: time.UnixMilli(1_700_000_600_000).UTC()},
		ServerAuthorized{Server: "notion"},
		AuthorizationFailed{Server: "notion", Reason: "the link expired"},

		SubmitPrompt{Text: "go"},
		ResolveApproval{ToolCall: call, Approved: true},
		NoteContext{Text: "use ripgrep"},
		Abort{Turn: turn},
		SuggestFinish{Turn: turn, Reason: "met"},
		Continue{Turn: turn, Approved: true},
		RequestRollback{Turn: turn, RevertFiles: true},
		ListSessions{},
		ListServers{},
		MeasureContext{},
		DeleteSession{Session: turn},
		RenameSession{Session: turn, Name: "the sandbox bug"},
		ResetSession{},
		ResumeSession{Session: turn},
		LoadSession{Session: turn},
		RunCommand{Text: "git status"},
		CancelCommand{UserCommand: shell},
		StopAgent{Agent: agent},
		AuthorizeServer{Server: "notion"},
		OpenAuthorization{Server: "notion"},
	}
	require.Len(t, cases, len(codecs), "every kind needs a case here")

	for _, want := range cases {
		t.Run(string(want.Kind()), func(t *testing.T) {
			payload, err := Encode(want)
			require.NoError(t, err)

			got, err := Decode(want.Kind(), payload)
			require.NoError(t, err)
			assert.Equal(t, want, got)
			assert.Equal(t, want.Kind(), got.Kind(), "the kind survives too")
		})
	}
}

func TestDecode_RefusesAnUnknownKind(t *testing.T) {
	_, err := Decode("no.such.thing", []byte(`{}`))
	assert.ErrorContains(t, err, "no type registered")
}

// A store lifts these out as columns, so they have to come off the
// event without a second list of which types carry what.
func TestSubject_ReadsTurnToolCallAndAgent(t *testing.T) {
	turn, call, agent := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	gotTurn, gotCall, gotAgent := Subject(TurnStarted{Turn: turn})
	assert.Equal(t, []uuid.UUID{turn, uuid.Nil, uuid.Nil}, []uuid.UUID{gotTurn, gotCall, gotAgent})

	gotTurn, gotCall, gotAgent = Subject(ToolCallEnded{ToolCall: call})
	assert.Equal(t, []uuid.UUID{uuid.Nil, call, uuid.Nil}, []uuid.UUID{gotTurn, gotCall, gotAgent})

	gotTurn, gotCall, gotAgent = Subject(ToolCallProposed{ToolCall: call, Agent: agent})
	assert.Equal(t, []uuid.UUID{uuid.Nil, call, agent}, []uuid.UUID{gotTurn, gotCall, gotAgent})

	gotTurn, gotCall, gotAgent = Subject(Notice{Text: "about nothing"})
	assert.Equal(t, []uuid.UUID{uuid.Nil, uuid.Nil, uuid.Nil}, []uuid.UUID{gotTurn, gotCall, gotAgent})
}

func typeName(e Event) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", e), "event.")
}
