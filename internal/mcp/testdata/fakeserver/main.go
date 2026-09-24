// Command fakeserver is a stdio MCP server for the tests.
package main

import (
	"context"
	"os"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	s := sdk.NewServer(&sdk.Implementation{Name: "fakeserver", Version: "1"}, nil)
	s.AddTool(&sdk.Tool{
		Name:        "echo",
		Description: "echoes a fixed line, plus whatever DETENT_MARKER holds",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{
			&sdk.TextContent{Text: "from a real subprocess, env=" + os.Getenv("DETENT_MARKER")},
		}}, nil
	})
	if err := s.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		os.Exit(1)
	}
}
