package mcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/tool"
)

// Invokers answers Calls with no command, routing each to the server
// that offered it. Safe for concurrent use: servers connect late.
type Invokers struct {
	mu    sync.RWMutex
	tools map[string]Tool
	// status is every configured server from the moment it is known,
	// so /mcp lists one that has not answered yet.
	status []event.ServerSummary
}

// Status is what /mcp draws, sorted by name the way they connect.
func (i *Invokers) Status() []event.ServerSummary {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return append([]event.ServerSummary(nil), i.status...)
}

// seed lists every server before any of them is dialled.
func (i *Invokers) seed(s []event.ServerSummary) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.status = s
}

// settle replaces a seeded entry with what dialling found.
func (i *Invokers) settle(s event.ServerSummary) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for n, old := range i.status {
		if old.Name == s.Name {
			i.status[n] = s
			return
		}
	}
	i.status = append(i.status, s)
}

// setAuth records where a server's sign-in stands, for /mcp to draw.
func (i *Invokers) setAuth(server, auth string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for n := range i.status {
		if i.status[n].Name == server {
			i.status[n].Auth = auth
		}
	}
}

func NewInvokers() *Invokers { return &Invokers{tools: map[string]Tool{}} }

// Add records what Register returned, which carries its own routing.
func (i *Invokers) Add(tools ...Tool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, t := range tools {
		i.tools[t.name] = t
	}
}

// Invoke answers one Call. A tool nothing owns comes back as a result
// saying so, never a Go error the Turn would end on.
func (i *Invokers) Invoke(ctx context.Context, c tool.Call) capture.Result {
	i.mu.RLock()
	t, ok := i.tools[c.Tool]
	i.mu.RUnlock()
	if !ok {
		return capture.Result{ExitCode: 1, Stderr: fmt.Sprintf("no connected server offers %q", c.Tool)}
	}
	return t.server.Call(ctx, t.remote, c.Args)
}

// Servers is every server behind these tools, deduplicated.
func (i *Invokers) Servers() []*Server {
	i.mu.RLock()
	defer i.mu.RUnlock()
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
