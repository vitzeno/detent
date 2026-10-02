package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/auth"
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
	// OAuth tunes a remote server's sign-in. Nil still signs in on a 401.
	OAuth *OAuth `json:"oauth"`
	// Auth is another client's field, read for what it can give OAuth.
	Auth foreignAuth `json:"auth"`
}

// transport is how this server is reached, and says so when the
// config describes no server at all or two of them. oauth signs in.
func (c Config) transport(oauth auth.OAuthHandler) (sdk.Transport, error) {
	switch {
	case c.Command != "" && c.URL != "":
		return nil, errors.New("set command or url, not both")

	case c.URL != "":
		// Absent type means stdio everywhere else, so a url without
		// one is a remote server that forgot to say which kind.
		switch c.Type {
		case "", "http", "streamable-http":
			return HTTP{URL: c.URL, Headers: c.Headers, Auth: oauth}.Transport(), nil
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

// ConnectOption adds to how ConnectAll dials.
type ConnectOption func(*connecting)

type connecting struct{ signins *SignIns }

// WithSignIns lets a server that answers 401 ask a human to sign in.
// Without it, such a server connects on a saved token or fails.
func WithSignIns(s *SignIns) ConnectOption { return func(c *connecting) { c.signins = s } }

// ConnectAll lists every server first, then fills in the enabled ones:
// dialled concurrently, registered and reported as each settles.
func ConnectAll(ctx context.Context, reg *tool.Registry, in *Invokers, servers map[string]Config,
	report func(event.ServerSummary), opts ...ConnectOption) []error {
	var how connecting
	for _, o := range opts {
		o(&how)
	}
	names := sorted(servers)
	seeded := make([]event.ServerSummary, len(names))
	for i, name := range names {
		c := servers[name]
		seeded[i] = event.ServerSummary{Name: name, Command: describeConfig(c), Disabled: c.Disabled}
	}
	in.seed(seeded)

	// Registered as each settles, not after all: a server waiting ten
	// minutes for a sign-in must not keep every other's tools away.
	// One lock, so a listing drawn from a report cannot go backwards.
	var reporting sync.Mutex
	failed := make([]error, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		if servers[name].Disabled {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := servers[name]
			got := dial(ctx, name, c, how.signins)
			st := summarise(seeded[i], c, got)
			reporting.Lock()
			defer reporting.Unlock()
			switch {
			case got.err != nil:
				failed[i] = got.err
			case got.server != nil:
				in.Add(Register(reg, got.server, got.tools)...)
			}
			in.settle(st)
			if report != nil {
				report(st)
			}
		}()
	}
	wg.Wait()
	var errs []error
	for _, err := range failed {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// summarise is what /mcp shows of a server once dialling it settled.
func summarise(st event.ServerSummary, c Config, got result) event.ServerSummary {
	st.Connected, st.Tools = got.err == nil, len(got.tools)
	if got.err != nil {
		st.Err = got.err.Error()
	}
	if got.oauth {
		st.Auth = event.AuthSignedOut
		if got.err == nil {
			st.Auth = event.AuthSignedIn
		}
	}
	return st
}

// Redialer dials one server again after forgetting its token, so an
// AuthorizeServer always ends in a fresh sign-in, never a silent reuse.
func Redialer(ctx context.Context, reg *tool.Registry, in *Invokers, servers map[string]Config,
	signins *SignIns) func(string) error {
	return func(name string) error {
		c, ok := servers[name]
		switch {
		case !ok:
			return fmt.Errorf("no MCP server is called %s", name)
		case c.URL == "":
			return fmt.Errorf("%s is launched, not reached: it holds its own credentials", name)
		case c.Disabled:
			return fmt.Errorf("%s is disabled in the config", name)
		case signins.waitingFor(name):
			return fmt.Errorf("a sign-in for %s is already waiting", name)
		}
		if err := signins.tokens.Forget(name); err != nil {
			return err
		}
		got := dial(ctx, name, c, signins)
		st := summarise(event.ServerSummary{Name: name, Command: describeConfig(c)}, c, got)
		if got.err == nil {
			// Swapped, not added: the old session's tools go with it, or
			// a re-registered tool would rename itself beside the stale one.
			old, names := in.drop(name)
			reg.Unregister(names...)
			in.Add(Register(reg, got.server, got.tools)...)
			if old != nil {
				_ = old.Close()
			}
		}
		in.settle(st)
		signins.publish(event.ServersListed{Servers: in.Status()})
		return got.err
	}
}

// result is one server's answer.
type result struct {
	server *Server
	tools  []*sdk.Tool
	err    error
	// oauth is set when signing in played a part: a token, or a link shown.
	oauth bool
}

// dial connects one server and asks what it offers.
func dial(ctx context.Context, name string, c Config, signins *SignIns) result {
	// Every remote server can sign in: a 401 says it must, as Claude Code,
	// Gemini CLI and Codex all read it. One that never sends one never does.
	var oauth auth.OAuthHandler
	var h *serial
	if c.URL != "" {
		if signins == nil {
			signins = NewSignIns(nil, nil, Tokens{Dir: TokensDir()}, nil)
		}
		var err error
		if h, err = oauthHandler(name, c.OAuth, HTTP{Headers: c.Headers}.client(), signins); err != nil {
			return result{err: err}
		}
		oauth = h
	}
	got := connect(ctx, name, c, oauth)
	if h != nil {
		got.oauth = h.used(ctx)
	}
	return got
}

// connect dials and lists what the server offers.
func connect(ctx context.Context, name string, c Config, oauth auth.OAuthHandler) result {
	t, err := c.transport(oauth)
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
