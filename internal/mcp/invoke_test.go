package mcp

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/tool"
)

// Prepare to result, against a server that actually answers.
func TestInvoke_AnsweredByTheServerThatOfferedIt(t *testing.T) {
	s := serve(t, text("greet", &sdk.TextContent{Text: "hello there"}))
	reg, in := wired(t, s)

	call, err := reg.Prepare("fake__greet", map[string]any{"who": "world"})
	require.NoError(t, err)

	res := in.Invoke(context.Background(), call)
	assert.Equal(t, "hello there", res.Stdout)
	assert.Zero(t, res.ExitCode)
}

// The namespaced name is detent's. The server only knows its own.
func TestInvoke_CallsTheServersOwnName(t *testing.T) {
	var got string
	s := serve(t, fake{name: "create_issue", handle: func(_ context.Context, r *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		got = r.Params.Name
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "filed"}}}, nil
	}})
	reg, in := wired(t, s)

	call, err := reg.Prepare("fake__create_issue", nil)
	require.NoError(t, err)
	in.Invoke(context.Background(), call)

	assert.Equal(t, "create_issue", got, "the namespaced name was sent to the server")
}

// A tool nothing owns is a result the model can read, not an error.
func TestInvoke_AnUnknownToolIsAResult(t *testing.T) {
	s := serve(t, text("alpha"))
	_, in := wired(t, s)

	res := in.Invoke(context.Background(), tool.Call{Tool: "gone__missing", Executor: "gone"})
	assert.Equal(t, 1, res.ExitCode)
	assert.Contains(t, res.Stderr, "gone__missing")
}

// Close has to reach every server, since each stdio one is a process.
func TestInvokers_CloseEndsEveryServer(t *testing.T) {
	a := serveAs(t, "alpha", 0, text("one"))
	b := serveAs(t, "beta", 0, text("two"))

	in := NewInvokers()
	for _, s := range []*Server{a, b} {
		tools, err := s.Tools(context.Background())
		require.NoError(t, err)
		in.Add(Register(tool.Standard(), s, tools)...)
	}
	require.Len(t, in.Servers(), 2)
	require.NoError(t, in.Close())

	assert.NotZero(t, a.Call(context.Background(), "one", nil).ExitCode)
	assert.NotZero(t, b.Call(context.Background(), "two", nil).ExitCode)
}

// wired is a registry and an Invokers built from one live server,
// which is how a session will have them.
func wired(t *testing.T, s *Server) (*tool.Registry, *Invokers) {
	t.Helper()
	tools, err := s.Tools(context.Background())
	require.NoError(t, err)

	reg := tool.Standard()
	in := NewInvokers()
	in.Add(Register(reg, s, tools)...)
	return reg, in
}
