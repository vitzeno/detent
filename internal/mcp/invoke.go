package mcp

import (
	"context"
	"fmt"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/tool"
)

// Invokers answers Calls with no command, routing each to the server
// that offered it. Satisfies engine.Invoker structurally.
type Invokers struct {
	tools map[string]Tool
	// status is every configured server, connected or not: one that
	// is missing is the thing a human needs to be told about.
	status []event.ServerSummary
}

// Status is what /mcp draws, sorted by name the way they connect.
func (i *Invokers) Status() []event.ServerSummary { return i.status }

func NewInvokers() *Invokers { return &Invokers{tools: map[string]Tool{}} }

// Add records what Register returned, which carries its own routing.
func (i *Invokers) Add(tools ...Tool) {
	for _, t := range tools {
		i.tools[t.name] = t
	}
}

// Invoke answers one Call. A tool nothing owns comes back as a result
// saying so, never a Go error the Turn would end on.
func (i *Invokers) Invoke(ctx context.Context, c tool.Call) capture.Result {
	t, ok := i.tools[c.Tool]
	if !ok {
		return capture.Result{ExitCode: 1, Stderr: fmt.Sprintf("no connected server offers %q", c.Tool)}
	}
	return t.server.Call(ctx, t.remote, c.Args)
}

// Servers is every server behind these tools, deduplicated.
func (i *Invokers) Servers() []*Server {
	seen := map[string]bool{}
	var out []*Server
	for _, t := range i.tools {
		if !seen[t.server.Name] {
			seen[t.server.Name] = true
			out = append(out, t.server)
		}
	}
	return out
}

// Close ends every session, and each stdio server's process with it.
func (i *Invokers) Close() error {
	var err error
	for _, s := range i.Servers() {
		if e := s.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}
