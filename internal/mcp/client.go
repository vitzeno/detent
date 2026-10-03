// Package mcp reaches tools an MCP server holds, so a credentialed
// service can be called without its credentials entering the sandbox.
// Tool calls run in this process, not the sandbox.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/version"
)

const (
	// maxPages bounds following a cursor, against a server that never ends its list.
	maxPages = 100
	// stderrTail is how much of a server's stderr is kept: enough for the
	// line that says why it stopped.
	stderrTail = 1024
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
		if st, ok := t.(*stdioTransport); ok {
			if said := st.stderr.String(); said != "" {
				return nil, fmt.Errorf("mcp: connect %s: %w; it said: %s", name, err, said)
			}
		}
		return nil, fmt.Errorf("mcp: connect %s: %w", name, err)
	}
	return &Server{Name: name, session: session}, nil
}

// Tools is everything the server offers, following the cursor: a
// server with more tools than one page is not unusual.
func (s *Server) Tools(ctx context.Context) ([]*sdk.Tool, error) {
	var out []*sdk.Tool
	var cursor string
	seen := map[string]bool{}
	for range maxPages {
		page, err := s.session.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("mcp: list tools on %s: %w", s.Name, err)
		}
		out = append(out, page.Tools...)
		if page.NextCursor == "" || seen[page.NextCursor] {
			return out, nil
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return nil, fmt.Errorf("mcp: list tools on %s: more than %d pages", s.Name, maxPages)
}

// Call runs one tool. A server that refuses, fails or has gone away
// comes back as a Result, the way every bad call in detent does.
func (s *Server) Call(ctx context.Context, name string, args map[string]any) capture.Result {
	start := time.Now()
	res, err := s.session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		// Named with how long it ran, so a slow server is not read as a broken one.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return capture.Result{ExitCode: 1, Stderr: fmt.Sprintf("%s: no answer after %s, stopped",
				s.Name, time.Since(start).Round(time.Second))}
		}
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
	// Transport takes no ctx: the session's Close ends the process group instead.
	cmd := exec.Command(s.Command, s.Args...) //nolint:noctx // see above
	// Never nil, which exec reads as the whole of detent's environment.
	cmd.Env = append([]string{}, s.Env...)
	st := &stdioTransport{stderr: &tail{max: stderrTail}}
	cmd.Stderr = st.stderr
	// A child that inherited stderr must not hold Wait open once the server exits.
	cmd.WaitDelay = time.Second
	ownGroup(cmd)
	st.CommandTransport = &sdk.CommandTransport{Command: cmd}
	return st
}

// stdioTransport keeps what the server said on stderr, for when it fails
// to start, and ends its whole process group with it.
type stdioTransport struct {
	*sdk.CommandTransport
	stderr *tail
}

func (t *stdioTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	conn, err := t.CommandTransport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return groupConn{Connection: conn, end: joinGroup(t.Command)}, nil
}

// groupConn ends what a wrapper such as npx or uvx started, which a
// signal to the wrapper alone can leave running.
type groupConn struct {
	sdk.Connection
	end func()
}

func (c groupConn) Close() error {
	err := c.Connection.Close()
	c.end()
	return err
}

// tail keeps the last max bytes written to it.
type tail struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(strings.ToValidUTF8(string(t.buf), ""))
}
