package mcp

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/tool"
)

func TestToolName_IsNamespacedAndEndpointLegal(t *testing.T) {
	for _, c := range []struct{ server, remote, want string }{
		{"github", "create_issue", "github__create_issue"},
		{"admin", "admin.tools.list", "admin__admin_tools_list"},
		{"my server", "search", "my_server__search"},
		{"", "search", "tool__search"},
	} {
		got := toolName(c.server, c.remote)
		assert.Equal(t, c.want, got)
		legal(t, got)
	}
}

func TestToolName_TruncatesToWhatAnEndpointTakes(t *testing.T) {
	got := toolName("server", strings.Repeat("x", 200))
	assert.Len(t, got, 64)
	legal(t, got)
}

// A long server name must not crowd out the tool's own, or every tool
// it offers truncates to the same prefix.
func TestToolName_KeepsTheToolsOwnNameUnderALongServerName(t *testing.T) {
	a := toolName(strings.Repeat("s", 80), "create_issue")
	b := toolName(strings.Repeat("s", 80), "list_issues")
	assert.NotEqual(t, a, b)
	assert.True(t, strings.HasSuffix(a, "__create_issue"), a)
	legal(t, a)
}

// One verbose server must not swell every request, and a schema an
// endpoint would refuse must not fail every Step.
func TestSpecOf_BoundsWhatAServerSends(t *testing.T) {
	long := specOf("srv", &sdk.Tool{Name: "x", Description: strings.Repeat("é", 3000)}, true)
	assert.LessOrEqual(t, len(long.Description), maxDescription+len(" [truncated]"))
	assert.True(t, utf8.ValidString(long.Description))
	assert.Contains(t, long.Description, "[truncated]")

	for _, schema := range []any{
		map[string]any{"type": "string"},
		map[string]any{},
		nil,
	} {
		got := specOf("srv", &sdk.Tool{Name: "x", InputSchema: schema}, true).Raw
		assert.Equal(t, "object", got["type"], "%v", schema)
	}
}

// A server that offers a tool called bash must not become bash.
func TestRegister_ABuiltInAlwaysWins(t *testing.T) {
	reg := tool.Standard()
	before, _ := reg.Lookup("bash")

	s := &Server{Name: "bash"} // namespaces to bash__bash, not bash
	added := registerTools(reg, s, []*sdk.Tool{{Name: "bash", InputSchema: map[string]any{"type": "object"}}}, true)

	after, _ := reg.Lookup("bash")
	assert.Equal(t, before, after, "a server replaced a built-in")
	assert.NotContains(t, names(added), "bash")
}

// Truncation makes collisions likelier than the namespace alone, and
// registering must only ever add.
func TestRegister_RenamesRatherThanReplaces(t *testing.T) {
	reg := tool.Standard()
	s := &Server{Name: "srv"}
	long := strings.Repeat("y", 200)

	first := registerTools(reg, s, []*sdk.Tool{{Name: long, InputSchema: map[string]any{"type": "object"}}}, true)
	second := registerTools(reg, s, []*sdk.Tool{{Name: long, InputSchema: map[string]any{"type": "object"}}}, true)

	require.Len(t, first, 1)
	require.Len(t, second, 1)
	assert.NotEqual(t, first[0].Name(), second[0].Name(), "the second registration replaced the first")
	legal(t, second[0].Name())

	for _, n := range append(names(first), names(second)...) {
		_, ok := reg.Lookup(n)
		assert.True(t, ok, "%q was reported but not registered", n)
	}
}

// The server's schema reaches the model as it came: rewriting an
// arbitrary JSON Schema correctly is its own project.
func TestRegister_PassesTheSchemaThrough(t *testing.T) {
	reg := tool.Standard()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query":  map[string]any{"type": "string"},
			"limit":  map[string]any{"type": "integer", "minimum": 1},
			"filter": map[string]any{"anyOf": []any{map[string]any{"type": "string"}}},
		},
		"required": []any{"query"},
	}
	added := registerTools(reg, &Server{Name: "srv"}, []*sdk.Tool{
		{Name: "search", Description: "finds things", InputSchema: schema},
	}, true)
	require.Len(t, added, 1)

	tl, ok := reg.Lookup(added[0].Name())
	require.True(t, ok)
	assert.Equal(t, schema, tl.Describe().Raw)
	assert.Equal(t, "srv", tl.Describe().Executor)
	assert.Equal(t, "mcp · srv", tl.Describe().Group, "/context gives each server its own row")

	// And it reaches the model, not just the Spec: the two can
	// differ, and only the second one is what gets sent.
	assert.Equal(t, schema, parameters(t, reg, added[0].Name()))
}

// End to end against a real server: list, register, prepare.
func TestRegister_FromALiveServer(t *testing.T) {
	s := serve(t, text("alpha", &sdk.TextContent{Text: "ok"}), text("beta"))

	tools, err := s.Tools(context.Background())
	require.NoError(t, err)

	reg := tool.Standard()
	added := registerTools(reg, s, tools, true)
	assert.ElementsMatch(t, []string{"fake__alpha", "fake__beta"}, names(added))

	call, err := reg.Prepare("fake__alpha", nil)
	require.NoError(t, err)
	require.Equal(t, "fake", call.Executor)

	tl, ok := reg.Lookup("fake__alpha")
	require.True(t, ok)
	assert.Equal(t, "alpha", tl.(Tool).remote, "the server's own name was lost")
}

// Strict mode demands every property in required and optionals
// nullable, which an arbitrary schema will not have.
func TestSchemas_ARawSchemaIsNotOfferedAsStrict(t *testing.T) {
	reg := tool.Standard()
	added := registerTools(reg, &Server{Name: "srv"}, []*sdk.Tool{
		{Name: "search", InputSchema: map[string]any{"type": "object"}},
	}, true)

	var sawMCP, sawBuiltIn bool
	for _, s := range reg.Schemas() {
		fn := s["function"].(map[string]any)
		switch fn["name"] {
		case added[0].Name():
			sawMCP = true
			assert.Equal(t, false, fn["strict"])
		case "bash":
			sawBuiltIn = true
			assert.Equal(t, true, fn["strict"], "a built-in stopped being strict")
		}
	}
	assert.True(t, sawMCP && sawBuiltIn, "the schemas did not hold both kinds")
}

// Prepare must not validate a schema it did not build: the server
// published it and says what was wrong as a tool result.
func TestPrepare_DoesNotValidateARawSchema(t *testing.T) {
	reg := tool.Standard()
	added := registerTools(reg, &Server{Name: "srv"}, []*sdk.Tool{
		{Name: "search", InputSchema: map[string]any{"type": "object"}},
	}, true)

	call, err := reg.Prepare(added[0].Name(), map[string]any{"anything": "at all", "nested": map[string]any{"a": 1}})
	require.NoError(t, err, "arguments were rejected before the server ever saw them")
	assert.Equal(t, "srv", call.Executor)
	assert.Equal(t, "at all", call.Args["anything"])
}

// A built-in still rejects what it always rejected.
func TestPrepare_StillValidatesABuiltIn(t *testing.T) {
	reg := tool.Standard()
	_, err := reg.Prepare("read_file", map[string]any{"hallucinated": "x"})
	assert.Error(t, err)
}

// The Command is what a human reads before approving, so for a tool
// with nothing to run it describes the call.
func TestPrepare_CommandDescribesTheToolCall(t *testing.T) {
	reg := tool.Standard()
	added := registerTools(reg, &Server{Name: "github"}, []*sdk.Tool{
		{Name: "create_issue", InputSchema: map[string]any{"type": "object"}},
	}, true)

	call, err := reg.Prepare(added[0].Name(), map[string]any{"repo": "detent", "title": "it broke"})
	require.NoError(t, err)
	assert.Contains(t, call.Command, "github__create_issue")
	assert.Contains(t, call.Command, "repo=detent")
	assert.Contains(t, call.Command, `title="it broke"`)
}

// A shell tool must not pick up an executor, or every tool call would take
// the remote path.
func TestPrepare_AShellToolHasNoExecutor(t *testing.T) {
	call, err := tool.Standard().Prepare("bash", map[string]any{"command": "ls"})
	require.NoError(t, err)
	assert.Empty(t, call.Executor)
	assert.Equal(t, "ls", call.Command)
}

// legal is what a chat-completions request accepts, which is narrower
// than what MCP allows: no dots, and half the length.
// A server's read-only claim is taken only when trusted. A destructive one
// can only add caution, so it always shows.
func TestSpec_MutabilityFollowsTheServersHints(t *testing.T) {
	yes, no := true, false
	for _, tt := range []struct {
		name  string
		hints *sdk.ToolAnnotations
		trust bool
		want  string
	}{
		{"no annotations", nil, true, ""},
		{"no claim", &sdk.ToolAnnotations{}, true, ""},
		{"read-only", &sdk.ToolAnnotations{ReadOnlyHint: true}, true, event.MutRead},
		{"read-only, untrusted", &sdk.ToolAnnotations{ReadOnlyHint: true}, false, ""},
		{"destructive", &sdk.ToolAnnotations{DestructiveHint: &yes}, true, event.MutIrreversible},
		{"destructive, untrusted", &sdk.ToolAnnotations{DestructiveHint: &yes}, false, event.MutIrreversible},
		{"says not destructive", &sdk.ToolAnnotations{DestructiveHint: &no}, true, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := specOf("srv", &sdk.Tool{Name: "x", Annotations: tt.hints}, tt.trust)
			assert.Equal(t, tt.want, got.Mutability)
		})
	}
}

func legal(t *testing.T, name string) {
	t.Helper()
	require.LessOrEqual(t, len(name), 64, "%q is too long for an endpoint", name)
	require.NotEmpty(t, name)
	for _, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
		require.True(t, ok, "%q holds %q, which no endpoint accepts", name, r)
	}
}

// parameters is the schema a request would carry for one tool.
func parameters(t *testing.T, reg *tool.Registry, name string) map[string]any {
	t.Helper()
	for _, s := range reg.Schemas() {
		fn := s["function"].(map[string]any)
		if fn["name"] == name {
			return fn["parameters"].(map[string]any)
		}
	}
	t.Fatalf("%q is not in the schemas", name)
	return nil
}

// names is what Register registered, for asserting on.
func names(tools []Tool) []string {
	var out []string
	for _, t := range tools {
		out = append(out, t.Name())
	}
	return out
}
