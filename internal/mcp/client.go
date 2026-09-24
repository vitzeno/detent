// Package mcp reaches tools an MCP server holds, so a credentialed
// service can be called without its credentials entering the sandbox.
// Calls run in this process, not the sandbox.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/version"
)

// Server is one connected MCP server. Name is detent's key for it, not
// the server's own, which the spec says may collide.
type Server struct {
	Name    string
	session *sdk.ClientSession
}

// Connect opens a session and returns once the server is usable.
func Connect(ctx context.Context, name string, t sdk.Transport) (*Server, error) {
	if name == "" {
		return nil, errors.New("mcp: a server needs a name")
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "detent", Version: version.Number}, nil)
	session, err := client.Connect(ctx, t, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: connect %s: %w", name, err)
	}
	return &Server{Name: name, session: session}, nil
}

// Tools is everything the server offers, following the cursor: a
// server with more tools than one page is not unusual.
func (s *Server) Tools(ctx context.Context) ([]*sdk.Tool, error) {
	var out []*sdk.Tool
	var cursor string
	for {
		page, err := s.session.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("mcp: list tools on %s: %w", s.Name, err)
		}
		out = append(out, page.Tools...)
		if page.NextCursor == "" || page.NextCursor == cursor {
			return out, nil
		}
		cursor = page.NextCursor
	}
}

// Call runs one tool. A server that refuses, fails or has gone away
// comes back as a Result, the way every bad call in detent does.
func (s *Server) Call(ctx context.Context, name string, args map[string]any) capture.Result {
	res, err := s.session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return capture.Result{ExitCode: 1, Stderr: fmt.Sprintf("%s: %v", s.Name, err)}
	}
	return toResult(res)
}

// Close ends the session, and a stdio server's process with it.
func (s *Server) Close() error {
	if s.session == nil {
		return nil
	}
	return s.session.Close()
}

// Stdio is a server detent launches. Env is the whole environment, not
// an addition: credentials are named deliberately or not at all.
type Stdio struct {
	Command string
	Args    []string
	Env     []string
}

// Transport is separate from Connect so a test can supply its own.
func (s Stdio) Transport() sdk.Transport {
	cmd := exec.Command(s.Command, s.Args...)
	if s.Env != nil {
		cmd.Env = s.Env
	}
	return &sdk.CommandTransport{Command: cmd}
}
