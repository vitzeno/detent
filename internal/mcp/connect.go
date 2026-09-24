package mcp

import (
	"context"
	"os"
	"sort"

	"github.com/vitzeno/detent/internal/tool"
)

// Config is one server to launch, as the config file describes it.
type Config struct {
	Command  string
	Args     []string
	Env      map[string]string
	Disabled bool
}

// ConnectAll launches every enabled server and registers what each
// offers. One that will not start costs its tools, not the session.
func ConnectAll(ctx context.Context, reg *tool.Registry, servers map[string]Config) (*Invokers, []error) {
	in := NewInvokers()
	var errs []error
	// Sorted, so the tool order a model sees does not shuffle per run.
	for _, name := range sorted(servers) {
		c := servers[name]
		if c.Disabled || c.Command == "" {
			continue
		}
		s, err := Connect(ctx, name, Stdio{Command: c.Command, Args: c.Args, Env: environ(c.Env)}.Transport())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		tools, err := s.Tools(ctx)
		if err != nil {
			errs = append(errs, err)
			_ = s.Close()
			continue
		}
		in.Add(Register(reg, s, tools)...)
	}
	return in, errs
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
