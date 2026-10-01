package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/tool"
)

// Config is one server to launch, as the config file describes it.
type Config struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	// URL reaches a server rather than launching one. Set this or
	// Command, never both.
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	// Type names a remote transport. Absent means stdio, which is the
	// convention every other client follows.
	Type string `json:"type"`
	// Disabled keeps a server configured but unconnected.
	Disabled bool `json:"disabled"`
	// Auth signs in to a remote server with OAuth. Absent is headers only.
	Auth *Auth `json:"auth"`
}

// transport is how this server is reached, and says so when the
// config describes no server at all or two of them.
func (c Config) transport() (sdk.Transport, error) {
	switch {
	case c.Command != "" && c.URL != "":
		return nil, errors.New("set command or url, not both")

	case c.URL != "":
		// Absent type means stdio everywhere else, so a url without
		// one is a remote server that forgot to say which kind.
		switch c.Type {
		case "", "http", "streamable-http":
			return HTTP{URL: c.URL, Headers: c.Headers}.Transport(), nil
		}
		return nil, fmt.Errorf("transport %q is not supported; use http", c.Type)

	case c.Command != "":
		if c.Auth != nil {
			return nil, errors.New("auth is for a url; a launched server holds its own credentials")
		}
		if c.Type != "" && c.Type != "stdio" {
			return nil, fmt.Errorf("type %q takes a url, not a command", c.Type)
		}
		return Stdio{Command: c.Command, Args: c.Args, Env: environ(c.Env)}.Transport(), nil
	}
	return nil, errors.New("no command or url configured")
}

// ConnectAll lists every server first, then fills in the enabled ones:
// dialled concurrently, reported as each settles, registered by name.
func ConnectAll(ctx context.Context, reg *tool.Registry, in *Invokers, servers map[string]Config, report func(event.ServerSummary)) []error {
	names := sorted(servers)
	seeded := make([]event.ServerSummary, len(names))
	for i, name := range names {
		c := servers[name]
		seeded[i] = event.ServerSummary{Name: name, Command: describeConfig(c), Disabled: c.Disabled}
	}
	in.seed(seeded)

	dialled := make([]result, len(names))
	// Settling and reporting under one lock, so a listing drawn from
	// a report cannot go backwards when two servers answer together.
	var reporting sync.Mutex
	var wg sync.WaitGroup
	for i, name := range names {
		if servers[name].Disabled {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := dial(ctx, name, servers[name])
			dialled[i] = got

			st := seeded[i]
			st.Connected, st.Tools = got.err == nil, len(got.tools)
			if got.err != nil {
				st.Err = got.err.Error()
			}
			reporting.Lock()
			defer reporting.Unlock()
			in.settle(st)
			if report != nil {
				report(st)
			}
		}()
	}
	wg.Wait()

	var errs []error
	for i := range names {
		got := dialled[i]
		switch {
		case got.err != nil:
			errs = append(errs, got.err)
		case got.server != nil:
			in.Add(Register(reg, got.server, got.tools)...)
		}
	}
	return errs
}

// result is one server's answer, held so registering can be ordered.
type result struct {
	server *Server
	tools  []*sdk.Tool
	err    error
}

// dial connects one server and asks what it offers.
func dial(ctx context.Context, name string, c Config) result {
	t, err := c.transport()
	if err != nil {
		return result{err: fmt.Errorf("mcp: %s: %w", name, err)}
	}
	s, err := Connect(ctx, name, t)
	if err != nil {
		return result{err: err}
	}
	tools, err := s.Tools(ctx)
	if err != nil {
		_ = s.Close()
		return result{err: err}
	}
	return result{server: s, tools: tools}
}

// passThrough is what a process needs to run at all. Everything else
// is named in config, so detent's own keys do not follow a server in.
var passThrough = []string{"PATH", "HOME", "TMPDIR", "LANG", "USER"}

func environ(env map[string]string) []string {
	out := make([]string, 0, len(passThrough)+len(env))
	for _, k := range passThrough {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	for _, k := range sortedKeys(env) {
		out = append(out, k+"="+env[k])
	}
	return out
}

func sorted(m map[string]Config) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// describeConfig is what /mcp shows under a server's name: the thing
// it launches, or the thing it reaches.
func describeConfig(c Config) string {
	if c.URL != "" {
		return c.URL
	}
	return c.Command
}
