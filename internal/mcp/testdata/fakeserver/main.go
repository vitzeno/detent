// Command fakeserver is a stdio MCP server for the tests.
// FAKESERVER_MODE=none offers an empty list, and =fail exits saying why.
package main

import (
	"context"
	"fmt"
	"os"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	mode := os.Getenv("FAKESERVER_MODE")
	if mode == "fail" {
		fmt.Fprintln(os.Stderr, "fakeserver: FAKE_TOKEN is not set")
		os.Exit(1)
	}
	s := sdk.NewServer(&sdk.Implementation{Name: "fakeserver", Version: "1"},
		&sdk.ServerOptions{Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{}}})
	if mode != "none" {
		s.AddTool(&sdk.Tool{
			Name:        "echo",
			Description: "echoes a fixed line, plus whatever DETENT_MARKER holds",
			InputSchema: map[string]any{"type": "object"},
		}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{
				&sdk.TextContent{Text: "from a real subprocess, env=" + os.Getenv("DETENT_MARKER")},
			}}, nil
		})
	}
	if err := s.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		os.Exit(1)
	}
}
