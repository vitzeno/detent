package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

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
		if c.Type != "" && c.Type != "stdio" {
			return nil, fmt.Errorf("type %q takes a url, not a command", c.Type)
		}
		return Stdio{Command: c.Command, Args: c.Args, Env: environ(c.Env)}.Transport(), nil
	}
	return nil, errors.New("no command or url configured")
}

// ConnectAll launches every enabled server and registers what each
// offers. One that will not start costs its tools, not the session.
func ConnectAll(ctx context.Context, reg *tool.Registry, servers map[string]Config) (*Invokers, []error) {
	in := NewInvokers()
	var errs []error
	// Sorted, so the tool order a model sees does not shuffle per run.
	for _, name := range sorted(servers) {
		c := servers[name]
		st := event.ServerSummary{Name: name, Command: describeConfig(c), Disabled: c.Disabled}
		if !c.Disabled {
			tools, err := dial(ctx, reg, in, name, c)
			if err != nil {
				st.Err = err.Error()
				errs = append(errs, err)
			}
			st.Tools = tools
		}
		in.status = append(in.status, st)
	}
	return in, errs
}

// dial connects one server and registers what it offers, reporting
// how many so a human can see a server that answered with nothing.
func dial(ctx context.Context, reg *tool.Registry, in *Invokers, name string, c Config) (int, error) {
	t, err := c.transport()
	if err != nil {
		return 0, fmt.Errorf("mcp: %s: %w", name, err)
	}
	s, err := Connect(ctx, name, t)
	if err != nil {
		return 0, err
	}
	tools, err := s.Tools(ctx)
	if err != nil {
		_ = s.Close()
		return 0, err
	}
	in.Add(Register(reg, s, tools)...)
	return len(tools), nil
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
