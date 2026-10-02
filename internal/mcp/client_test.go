package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capture"
)

func TestServer_ListsWhatItOffers(t *testing.T) {
	s := serve(t, text("alpha"), text("beta"))

	tools, err := s.Tools(context.Background())
	require.NoError(t, err)

	var names []string
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	assert.ElementsMatch(t, []string{"alpha", "beta"}, names)
}

func TestServer_CallReturnsText(t *testing.T) {
	s := serve(t, text("greet", &sdk.TextContent{Text: "hello"}))

	res := s.Call(context.Background(), "greet", map[string]any{"who": "world"})
	assert.Equal(t, "hello", res.Stdout)
	assert.Zero(t, res.ExitCode)
}

// A tool error is the model's cue to correct itself.
func TestServer_ToolErrorIsANonZeroExit(t *testing.T) {
	s := serve(t, fake{name: "fails", handle: func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{
			IsError: true,
			Content: []sdk.Content{&sdk.TextContent{Text: "date must be in the future"}},
		}, nil
	}})

	res := s.Call(context.Background(), "fails", nil)
	assert.Equal(t, 1, res.ExitCode)
	assert.Contains(t, res.Stdout, "must be in the future")
}

// What the model got wrong comes back as a result, not a Go error.
func TestServer_BadCallsAreResultsNotErrors(t *testing.T) {
	s := serve(t, text("only_tool"))

	for _, c := range []struct {
		name string
		tool string
	}{
		{"a tool that does not exist", "nope"},
		{"an empty name", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := s.Call(context.Background(), c.tool, nil)
			assert.NotZero(t, res.ExitCode, "a bad call should read as a failure")
			assert.NotEmpty(t, res.Stdout+res.Stderr, "and should say what went wrong")
		})
	}
}

// A handler returning an error is a protocol error, and the model
// still has to be able to read it.
func TestServer_AHandlerThatFailsIsStillAResult(t *testing.T) {
	s := serve(t, fake{name: "boom", handle: func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return nil, errors.New("the upstream API is down")
	}})

	res := s.Call(context.Background(), "boom", nil)
	assert.NotZero(t, res.ExitCode)
	assert.Contains(t, res.Stdout+res.Stderr, "down")
}

// A server that has gone away must not take the Turn with it.
func TestServer_ACallAfterCloseIsAResult(t *testing.T) {
	s := serve(t, text("alpha", &sdk.TextContent{Text: "ok"}))
	require.NoError(t, s.Close())

	res := s.Call(context.Background(), "alpha", nil)
	assert.NotZero(t, res.ExitCode)
	assert.NotEmpty(t, res.Stderr)
}

func TestConnect_RefusesAnUnnamedServer(t *testing.T) {
	client, _ := sdk.NewInMemoryTransports()
	_, err := Connect(context.Background(), "", client)
	assert.Error(t, err)
}

// Without the cursor a server silently offers only its first page.
func TestServer_ToolsFollowsPagination(t *testing.T) {
	var many []fake
	for i := range 25 {
		many = append(many, text(fmt.Sprintf("tool_%03d", i)))
	}
	s := servePaged(t, 10, many...) // 3 pages, so the cursor matters

	tools, err := s.Tools(context.Background())
	require.NoError(t, err)
	assert.Len(t, tools, 25, "a page boundary lost tools")
}

func TestToResult_NonTextBlocksBecomeALineNotAPayload(t *testing.T) {
	res := toResult(&sdk.CallToolResult{Content: []sdk.Content{
		&sdk.TextContent{Text: "here is the chart"},
		&sdk.ImageContent{MIMEType: "image/png", Data: make([]byte, 12_700)},
		&sdk.AudioContent{MIMEType: "audio/wav", Data: make([]byte, 400)},
		&sdk.ResourceLink{URI: "file:///src/main.go"},
	}})

	assert.Equal(t, strings.Join([]string{
		"here is the chart",
		"[image image/png, 12.4KB]",
		"[audio audio/wav, 400B]",
		"[resource file:///src/main.go]",
	}, "\n"), res.Stdout)
}

// A server that inlines a file meant the model to read it.
func TestToResult_EmbeddedResourceKeepsItsText(t *testing.T) {
	res := toResult(&sdk.CallToolResult{Content: []sdk.Content{
		&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{
			URI: "file:///main.go", Text: "package main",
		}},
	}})
	assert.Contains(t, res.Stdout, "package main")
	assert.Contains(t, res.Stdout, "file:///main.go")
}

// For the servers that do not duplicate it into a text block.
func TestToResult_FallsBackToStructuredContent(t *testing.T) {
	res := toResult(&sdk.CallToolResult{
		StructuredContent: map[string]any{"temperature": 22.5},
	})
	assert.Contains(t, res.Stdout, "22.5")
}

// Losing the tail silently is how a model reasons off a lie.
func TestToResult_SaysWhenItTruncated(t *testing.T) {
	res := toResult(&sdk.CallToolResult{Content: []sdk.Content{
		&sdk.TextContent{Text: strings.Repeat("x", capture.MaxOutputBytes*2)},
	}})
	assert.Len(t, res.Stdout, capture.MaxOutputBytes)
	assert.True(t, res.Truncated, "the tail went without saying so")
}

// serve wires a real server to a real client over an in-memory pair,
// so these exercise the protocol rather than a fake of it.
func serve(t *testing.T, tools ...fake) *Server {
	return serveAs(t, "fake", 0, tools...)
}

// servePaged takes the page size: the SDK's default is 1000, and a
// test that never crosses a boundary proves nothing about the cursor.
func servePaged(t *testing.T, pageSize int, tools ...fake) *Server {
	return serveAs(t, "fake", pageSize, tools...)
}

// serveAs names the server, since detent keys servers by name and two
// sharing one is a case the config cannot produce.
func serveAs(t *testing.T, name string, pageSize int, tools ...fake) *Server {
	t.Helper()
	srv := sdk.NewServer(&sdk.Implementation{Name: name, Version: "1"},
		&sdk.ServerOptions{PageSize: pageSize})
	for _, tl := range tools {
		srv.AddTool(&sdk.Tool{
			Name:        tl.name,
			Description: tl.desc,
			InputSchema: map[string]any{"type": "object"},
		}, tl.handle)
	}

	client, server := sdk.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, server, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	s, err := Connect(ctx, name, client)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// fake is one tool the in-memory server offers.
type fake struct {
	name, desc string
	handle     sdk.ToolHandler
}

func text(name string, out ...sdk.Content) fake {
	return fake{name: name, desc: name + " does something", handle: func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: out}, nil
	}}
}
