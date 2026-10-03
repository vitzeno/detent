package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/tool"
)

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
	names := slices.Sorted(maps.Keys(servers))
	seeded := make([]event.ServerSummary, len(names))
	for i, name := range names {
		c := servers[name]
		seeded[i] = event.ServerSummary{Name: name, Command: describeConfig(c), Disabled: c.Disabled}
	}
	in.seed(seeded)

	// Registered as each settles, so a sign-in waiting on a human holds up no other
	// server. One lock, so a listing drawn from a report cannot go backwards.
	var reporting sync.Mutex
	failed := make([]error, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		if servers[name].Disabled {
			continue
		}
		// Claimed before any goroutine runs: a redial asked for meanwhile owns its status.
		if how.signins != nil && !how.signins.claim(name) {
			continue
		}
		wg.Go(func() {
			if how.signins != nil {
				defer how.signins.release(name)
			}
			c := servers[name]
			got := dial(ctx, name, c, how.signins)
			st := summarise(seeded[i], got)
			reporting.Lock()
			defer reporting.Unlock()
			switch {
			case got.err != nil:
				failed[i] = got.err
			case got.server != nil:
				in.register(reg, got.server, got.tools)
			}
			in.settle(st)
			if report != nil {
				report(st)
			}
		})
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

// Redialer dials one server again after forgetting its token, so an
// AuthorizeServer always ends in a fresh sign-in, never a silent reuse.
func Redialer(reg *tool.Registry, in *Invokers, servers map[string]Config,
	signins *SignIns) func(context.Context, string) error {
	return func(ctx context.Context, name string) error {
		c, ok := servers[name]
		switch {
		case signins == nil:
			return errors.New("signing in is not available here")
		case !ok:
			return fmt.Errorf("no MCP server is called %s", name)
		case c.URL == "":
			return fmt.Errorf("%s is launched, not reached: it holds its own credentials", name)
		case c.Disabled:
			return fmt.Errorf("%s is disabled in the config", name)
		case !signins.claim(name):
			return fmt.Errorf("%s is already being connected or signed in to", name)
		}
		defer signins.release(name)
		if err := signins.retire(name, c.URL); err != nil {
			return err
		}
		got := dial(ctx, name, c, signins)
		switch {
		case got.err == nil:
			// Swapped, not added: the old session's tools go with it, or
			// a re-registered tool would rename itself beside the stale one.
			if old := in.swap(reg, got.server, got.tools); old != nil {
				_ = old.Close()
			}
			in.settle(summarise(event.ServerSummary{Name: name, Command: describeConfig(c)}, got))
		case !in.connected(name):
			in.settle(summarise(event.ServerSummary{Name: name, Command: describeConfig(c)}, got))
		}
		// A failure leaves a session that still answers as it was, tools and status alike.
		signins.publish(event.ServersListed{Servers: in.Status()})
		return got.err
	}
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

// summarise is what /mcp shows of a server once dialling it settled.
func summarise(st event.ServerSummary, got result) event.ServerSummary {
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

// result is one server's answer.
type result struct {
	server *Server
	tools  []*sdk.Tool
	err    error
	// oauth is set when signing in played a part: a token, or a link shown.
	oauth bool
}

// dial connects one server and asks what it offers. Its error names no
// secret, since it goes on the bus and into the store.
func dial(parent context.Context, name string, c Config, signins *SignIns) result {
	ctx, cancel := withPatience(parent, connectTimeout)
	defer cancel()
	// Every remote server can sign in: a 401 says it must, as Claude Code,
	// Gemini CLI and Codex all read it. One that never sends one never does.
	var oauth auth.OAuthHandler
	var h *serial
	if c.URL != "" {
		if signins == nil {
			signins = NewSignIns(nil, nil, Tokens{Dir: TokensDir()}, nil)
		}
		var err error
		if h, err = oauthHandler(name, c.URL, c.OAuth, authClient(), signins); err != nil {
			return result{err: redact(c, err)}
		}
		oauth = h
	}
	got := connect(ctx, name, c, oauth)
	if got.err != nil && ctx.Err() != nil && parent.Err() == nil {
		got.err = fmt.Errorf("mcp: %s: %w", name, context.Cause(ctx))
	}
	if h != nil {
		got.oauth = h.used(ctx)
	}
	if got.err != nil {
		got.err = redact(c, got.err)
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
	for _, k := range slices.Sorted(maps.Keys(env)) {
		out = append(out, k+"="+env[k])
	}
	return out
}

// describeConfig is what /mcp shows under a server's name: the thing
// it launches, or the thing it reaches without what may hold a key.
func describeConfig(c Config) string {
	if c.URL != "" {
		return redactURL(c.URL)
	}
	return c.Command
}

// redactURL drops the parts of a URL a key travels in: a user and a query.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(a url that does not parse)"
	}
	u.User = nil
	u.Fragment, u.RawFragment = "", ""
	if u.RawQuery != "" {
		u.RawQuery = "…"
	}
	return u.String()
}

// redact takes out of err what the config may have filled from the
// environment: the URL's query and password, and every header value.
func redact(c Config, err error) error {
	msg := err.Error()
	var secrets []string
	if c.URL != "" {
		if u, perr := url.Parse(c.URL); perr == nil {
			if pw, ok := u.User.Password(); ok {
				secrets = append(secrets, pw)
			}
			for _, vs := range u.Query() {
				secrets = append(secrets, vs...)
			}
			secrets = append(secrets, u.RawQuery)
		}
	}
	for _, v := range c.Headers {
		secrets = append(secrets, v)
	}
	out := msg
	if c.URL != "" {
		out = strings.ReplaceAll(out, c.URL, redactURL(c.URL))
	}
	for _, s := range secrets {
		if s != "" {
			out = strings.ReplaceAll(out, s, "***")
		}
	}
	if out == msg {
		return err
	}
	return redacted{msg: out, err: err}
}

// redacted says what err says, less its secrets, and still unwraps to it.
type redacted struct {
	msg string
	err error
}

func (r redacted) Error() string { return r.msg }
func (r redacted) Unwrap() error { return r.err }
