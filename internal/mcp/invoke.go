package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/tool"
)

// Invokers answers Calls with no command, routing each to the server
// that offered it. Safe for concurrent use: servers connect late.
type Invokers struct {
	// registering makes choosing a free name and taking it one step.
	registering sync.Mutex

	mu    sync.RWMutex
	tools map[string]Tool
	// servers is every connected session, including one offering no tools.
	servers map[string]*Server
	// status holds every configured server from the start, so /mcp lists unanswered ones.
	status []event.ServerSummary
}

// NewInvokers returns an Invokers with no tools.
func NewInvokers() *Invokers {
	return &Invokers{tools: map[string]Tool{}, servers: map[string]*Server{}}
}

// Add records what Register returned, which carries its own routing.
func (i *Invokers) Add(tools ...Tool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, t := range tools {
		i.tools[t.name] = t
		i.servers[t.server.Name] = t.server
	}
}

// register adds a server and its tools, so a server with none is still closed.
func (i *Invokers) register(reg *tool.Registry, s *Server, tools []*sdk.Tool) {
	i.registering.Lock()
	defer i.registering.Unlock()
	i.keep(s)
	i.Add(Register(reg, s, tools)...)
}

// swap replaces a server's session and tools, handing back the old session to close.
func (i *Invokers) swap(reg *tool.Registry, s *Server, tools []*sdk.Tool) *Server {
	i.registering.Lock()
	defer i.registering.Unlock()
	old, names := i.drop(s.Name)
	reg.Unregister(names...)
	i.keep(s)
	i.Add(Register(reg, s, tools)...)
	return old
}

func (i *Invokers) keep(s *Server) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.servers[s.Name] = s
}

// connected reports whether a session for server is open.
func (i *Invokers) connected(server string) bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.servers[server] != nil
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

// Status is what /mcp draws, sorted by name the way they connect.
func (i *Invokers) Status() []event.ServerSummary {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return append([]event.ServerSummary(nil), i.status...)
}

// Servers is every connected server, by name.
func (i *Invokers) Servers() []*Server {
	i.mu.RLock()
	defer i.mu.RUnlock()
	out := make([]*Server, 0, len(i.servers))
	for _, name := range slices.Sorted(maps.Keys(i.servers)) {
		out = append(out, i.servers[name])
	}
	return out
}

// Close ends every session, and each stdio server's process with it.
// Concurrently, since one stuck server can take ten seconds to give up.
func (i *Invokers) Close() error {
	servers := i.Servers()
	errs := make([]error, len(servers))
	var wg sync.WaitGroup
	for n, s := range servers {
		wg.Go(func() { errs[n] = s.Close() })
	}
	wg.Wait()
	return errors.Join(errs...)
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

// drop removes a server's tools, handing back their names and the
// session to close.
func (i *Invokers) drop(server string) (*Server, []string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	old := i.servers[server]
	delete(i.servers, server)
	var names []string
	for name, t := range i.tools {
		if t.server.Name == server {
			names = append(names, name)
			delete(i.tools, name)
		}
	}
	return old, names
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
