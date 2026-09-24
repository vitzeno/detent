package mcp

import (
	"context"
	"os"
	"sort"

	"github.com/vitzeno/detent/event"
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
		st := event.ServerSummary{Name: name, Command: c.Command, Disabled: c.Disabled}
		switch {
		case c.Disabled:
		case c.Command == "":
			st.Err = "no command configured"
		default:
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
	s, err := Connect(ctx, name, Stdio{Command: c.Command, Args: c.Args, Env: environ(c.Env)}.Transport())
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
