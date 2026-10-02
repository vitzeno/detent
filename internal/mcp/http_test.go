package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/tool"
)

func TestHTTP_ListsAndCalls(t *testing.T) {
	ctx := context.Background()
	s, err := Connect(ctx, "remote", HTTP{URL: httpServer(t, nil)}.Transport())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	tools, err := s.Tools(ctx)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "ping", tools[0].Name)

	assert.Equal(t, "pong", s.Call(ctx, "ping", nil).Stdout)
}

// The SDK takes no headers, so a token only arrives if we put it on a
// client of our own.
func TestHTTP_CarriesTheConfiguredHeaders(t *testing.T) {
	var seen http.Header
	url := httpServer(t, &seen)

	s, err := Connect(context.Background(), "remote",
		HTTP{URL: url, Headers: map[string]string{"Authorization": "Bearer hunter2"}}.Transport())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	_, err = s.Tools(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "Bearer hunter2", seen.Get("Authorization"))
}

// A server reached over HTTP registers like any other.
func TestConnectAll_ReachesAnHTTPServer(t *testing.T) {
	reg := tool.Standard()
	in := NewInvokers()
	errs := ConnectAll(context.Background(), reg, in, map[string]Config{
		"remote": {URL: httpServer(t, nil)},
	}, nil)
	t.Cleanup(func() { _ = in.Close() })
	require.Empty(t, errs)

	call, err := reg.Prepare("remote__ping", nil)
	require.NoError(t, err)
	assert.Equal(t, "remote", call.Executor)
	assert.Equal(t, "pong", in.Invoke(context.Background(), call).Stdout)
}

// The page shows what it reaches, not an empty command column.
func TestConnectAll_StatusShowsTheURL(t *testing.T) {
	url := httpServer(t, nil)
	in := NewInvokers()
	_ = ConnectAll(context.Background(), tool.Standard(), in, map[string]Config{"remote": {URL: url}}, nil)
	t.Cleanup(func() { _ = in.Close() })

	require.Len(t, in.Status(), 1)
	assert.Equal(t, url, in.Status()[0].Command)
}

// httpServer runs a real MCP server over real HTTP, so these exercise
// the transport rather than a stand-in for it.
func httpServer(t *testing.T, seen *http.Header) string {
	t.Helper()
	srv := sdk.NewServer(&sdk.Implementation{Name: "over-http", Version: "1"}, nil)
	srv.AddTool(&sdk.Tool{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "pong"}}}, nil
		})

	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = r.Header.Clone()
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}
