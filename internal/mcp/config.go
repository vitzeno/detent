package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Config is one server, launched or reached, as the config file describes it.
type Config struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	// URL reaches a server rather than launching one: this or Command, never both.
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	// Type names a remote transport. Absent means stdio, as in every other client.
	Type string `json:"type"`
	// Disabled keeps a server configured but unconnected.
	Disabled bool `json:"disabled"`
	// OAuth tunes a remote server's sign-in. Nil still signs in on a 401.
	OAuth *OAuth `json:"oauth"`
	// Auth is another client's field, read for what it can give OAuth.
	Auth foreignAuth `json:"auth"`
}

// Project is the working directory's own file. The name and shape are
// what every MCP client reads.
const Project = ".mcp.json"

// Files are the user's own config files, nearest last.
func Files() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".config", "detent", "mcp.json")}
}

// Load merges the files that exist and then project, the approved bytes of
// ./.mcp.json, later winning. An entry is replaced whole, never half and half.
func Load(project []byte, paths ...string) (map[string]Config, error) {
	out := map[string]Config{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, fmt.Errorf("mcp: read %s: %w", path, err)
		}
		if err := merge(out, path, raw); err != nil {
			return nil, err
		}
	}
	if project != nil {
		if err := merge(out, Project, project); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func merge(into map[string]Config, path string, raw []byte) error {
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("mcp: %s: %w", path, err)
	}
	for name, c := range f.Servers {
		into[name] = c.expanded()
	}
	return nil
}

// OAuth is Claude Code's "oauth" object, for a sign-in discovery cannot
// settle alone. A remote server signs in on a 401 whether or not it is set.
type OAuth struct {
	// A client registered by hand, for a provider with no registration of its own.
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	// CallbackPort fixes the redirect's port for a provider that whitelists it. 0 is any.
	CallbackPort int    `json:"callbackPort"`
	Scopes       Scopes `json:"scopes"`
}

// Scopes reads "a b c", as Claude Code writes them, or ["a", "b"], as
// Gemini CLI does.
type Scopes []string

// UnmarshalJSON accepts either form.
func (s *Scopes) UnmarshalJSON(b []byte) error {
	var spaced string
	if json.Unmarshal(b, &spaced) == nil {
		*s = strings.Fields(spaced)
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err != nil {
		return errors.New(`scopes must be "a b" or ["a", "b"]`)
	}
	*s = list
	return nil
}

// foreignAuth reads another client's "auth" for Cursor's credentials, and
// never fails a load: one foreign entry must not stop every other server.
type foreignAuth struct{ cursor *OAuth }

func (a *foreignAuth) UnmarshalJSON(b []byte) error {
	var c struct {
		ClientID     string `json:"CLIENT_ID"`
		ClientSecret string `json:"CLIENT_SECRET"`
		Scopes       Scopes `json:"scopes"`
	}
	if json.Unmarshal(b, &c) == nil && c.ClientID != "" {
		a.cursor = &OAuth{ClientID: c.ClientID, ClientSecret: c.ClientSecret, Scopes: c.Scopes}
	}
	return nil
}

// file is the shape on disk. mcpServers is the key every client uses.
type file struct {
	Servers map[string]Config `json:"mcpServers"`
}

// expanded resolves ${VAR} through the environment, so a file safe to
// commit can still name a token it does not hold.
func (c Config) expanded() Config {
	c.Command = expand(c.Command)
	c.URL = expand(c.URL)
	c.Args = expandAll(c.Args)
	c.Env = expandMap(c.Env)
	c.Headers = expandMap(c.Headers)
	if c.OAuth == nil {
		c.OAuth = c.Auth.cursor
	}
	if c.OAuth != nil {
		o := *c.OAuth
		o.ClientID, o.ClientSecret = expand(o.ClientID), expand(o.ClientSecret)
		c.OAuth = &o
	}
	return c
}

// expand replaces ${VAR} and ${VAR:-default}. Only the braced form:
// a bare $ is a literal, which paths and passwords rely on.
func expand(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "${")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.IndexByte(s[i+2:], '}')
		if j < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(lookup(s[i+2 : i+2+j]))
		s = s[i+3+j:]
	}
}

func lookup(ref string) string {
	name, fallback, hasFallback := strings.Cut(ref, ":-")
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v
	}
	if hasFallback {
		return fallback
	}
	return ""
}

func expandAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, expand(s))
	}
	return out
}

func expandMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = expand(v)
	}
	return out
}
