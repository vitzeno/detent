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
	shell := uuid.Must(uuid.NewV7())
	spec := &viewspec.Spec{Blocks: []viewspec.Block{{Kind: "log"}}}

	cases := []Event{
		SessionStarted{Session: turn, Model: "m", Sandbox: true, Network: true, MaxSteps: 50, Instructions: []string{"AGENTS.md"}},
		SessionResumed{Session: turn, Records: 412, Sandbox: true},
		TurnStarted{Turn: turn, N: 3, Prompt: "do it"},
		TurnEnded{Turn: turn, Reason: EndDone, Summary: "did it",
			Usage: Usage{PromptTokens: 7, CompletionTokens: 2, Latency: 90 * time.Millisecond, Model: "m"}},
		CheckpointTaken{Turn: turn, Snapshot: "snap", Tree: "tree"},
		BoundReached{Turn: turn, Steps: 50, Calls: 9},
		RolledBack{Turn: turn, RevertFiles: true},
		SessionReset{},
		StepStarted{Turn: turn, Step: step, N: 2},
		StepEnded{Turn: turn, Step: step, Calls: 3, Usage: Usage{PromptTokens: 1}},
		ModelText{Turn: turn, Step: step, Text: "prose"},
		Appended{Turn: turn, Step: step, Messages: []Message{
			{Role: RoleAssistant, Content: "looking", Calls: []ToolCall{
				{ID: "c1", Name: "bash", Args: map[string]any{"command": "ls"}, Err: "boom"}}},
			{Role: RoleTool, CallID: "c1", Content: "out"},
		}},
		Compacted{Turn: turn, Dropped: 12, Note: "summary"},
		ContextMeasured{Budget: 200, Total: 41, Exact: true, Growth: 2,
			Fixed:   []ContextPart{{Name: "tools", Detail: "9 built-in", Tokens: 3}},
			History: []ContextPart{{Name: "fix it", Tokens: 9, N: 4, Open: true}}},
		CallProposed{Call: call, Step: step, Tool: "bash", Args: map[string]any{"command": "ls"},
			Rationale: "why", Renders: RendersMarkdown, Executor: "github"},
		CallAssessed{Call: call, Risk: Risk{Dangerous: true, Mutability: MutSystem,
			ScopeRisk: 0.8, Note: "n", FromJudge: true}},
		ApprovalAsked{Call: call, Tool: "bash", Args: map[string]any{"command": "rm"},
			Rationale: "r", Risk: Risk{Dangerous: true}},
		CallStarted{Call: call, Runner: "sandbox"},
		OutputChunk{Call: call, Line: "a line", Stderr: true},
		CallEnded{Call: call, Took: 21 * time.Millisecond, Result: Result{
			ExitCode: 2, Stdout: "o", Stderr: "e", Truncated: true, Err: "x"}},
		CallJudged{Call: call, Status: "failed", RenderKind: "errors",
			Attention: 0.9, GoalAchieved: 0.1, FromJudge: true},
		ViewReady{Call: call, Spec: spec, Source: "composed"},
		ShellStarted{Shell: shell, Command: "git status", Runner: "sandbox"},
		ShellEnded{Shell: shell, Took: 8 * time.Millisecond, Result: Result{
			ExitCode: 1, Stdout: "o", Stderr: "e", Truncated: true, Err: "x"}},
		Notice{Level: "warn", Text: "careful"},
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
		ResolveApproval{Call: call, Approved: true},
		NoteContext{Text: "use ripgrep"},
		Abort{Turn: turn},
		RequestStop{Turn: turn, Reason: "met"},
		Continue{Turn: turn, Approved: true},
		RequestRollback{Turn: turn, RevertFiles: true},
		ListSessions{},
		ListServers{},
		MeasureContext{},
		DeleteSession{Session: turn},
		RenameSession{Session: turn, Name: "the sandbox bug"},
		ResetSession{},
		RunCommand{Text: "git status"},
		CancelCommand{Shell: shell},
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
func TestSubject_ReadsTurnAndCall(t *testing.T) {
	turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	gotTurn, gotCall := Subject(TurnStarted{Turn: turn})
	assert.Equal(t, turn, gotTurn)
	assert.Equal(t, uuid.Nil, gotCall)

	gotTurn, gotCall = Subject(CallEnded{Call: call})
	assert.Equal(t, uuid.Nil, gotTurn)
	assert.Equal(t, call, gotCall)

	gotTurn, gotCall = Subject(Notice{Text: "about nothing"})
	assert.Equal(t, uuid.Nil, gotTurn)
	assert.Equal(t, uuid.Nil, gotCall)
}

func typeName(e Event) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", e), "event.")
}
