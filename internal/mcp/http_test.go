package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// Configured headers are for the configured server: a redirect to
// another host does not carry them, and a signed-in token is not overwritten.
func TestHTTP_HeadersStayWithTheirOrigin(t *testing.T) {
	var elsewhere http.Header
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		elsewhere = r.Header.Clone()
	}))
	t.Cleanup(other.Close)
	var home http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		home = r.Header.Clone()
		http.Redirect(w, r, other.URL+"/away", http.StatusFound)
	}))
	t.Cleanup(server.Close)

	client := HTTP{URL: server.URL + "/mcp", Headers: map[string]string{
		"X-Api-Key": "hunter2", "Authorization": "Bearer static"}}.client()
	get := func(auth string) {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/mcp", nil)
		require.NoError(t, err)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}

	get("")
	assert.Equal(t, "hunter2", home.Get("X-Api-Key"))
	assert.Equal(t, "Bearer static", home.Get("Authorization"))
	require.NotNil(t, elsewhere, "the redirect was not followed")
	assert.Empty(t, elsewhere.Get("X-Api-Key"))
	assert.Empty(t, elsewhere.Get("Authorization"))

	get("Bearer signed-in")
	assert.Equal(t, "Bearer signed-in", home.Get("Authorization"))
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

// A key in the URL or a header goes nowhere that is published: not the
// status /mcp draws, and not the error a failed dial reports.
func TestConnectAll_PublishesNoSecretAConfigCarries(t *testing.T) {
	refused := httptest.NewServer(http.NotFoundHandler())
	refused.Close()
	u, err := url.Parse(refused.URL)
	require.NoError(t, err)
	u.User = url.UserPassword("me", "pa55word")
	u.Path, u.RawQuery = "/mcp", "api_key=s3cret"

	in := NewInvokers()
	errs := ConnectAll(context.Background(), tool.Standard(), in, map[string]Config{
		"keyed": {URL: u.String(), Headers: map[string]string{"X-Api-Key": "hunter2"}},
	}, nil, WithSignIns(NewSignIns(nil, nil, NewTokens(t.TempDir()), nil)))
	require.Len(t, errs, 1)

	st := in.Status()[0]
	published := st.Command + " " + st.Err + " " + errs[0].Error()
	assert.NotEmpty(t, st.Err)
	for _, secret := range []string{"s3cret", "pa55word", "hunter2"} {
		assert.NotContains(t, published, secret)
	}
	assert.Contains(t, st.Command, refused.Listener.Addr().String(), "the host is still shown")
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
