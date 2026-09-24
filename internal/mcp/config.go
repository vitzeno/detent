package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Files are where servers are configured, nearest last. The name and
// shape are what every MCP client reads, so one file serves both.
func Files() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".config", "detent", "mcp.json"))
	}
	return append(out, ".mcp.json")
}

// Load merges the files that exist, later winning. An entry is
// replaced whole: half of one config and half of another is nobody's.
func Load(paths ...string) (map[string]Config, error) {
	out := map[string]Config{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		switch {
		case os.IsNotExist(err):
			continue
		case err != nil:
			return nil, fmt.Errorf("mcp: read %s: %w", path, err)
		}
		var f file
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("mcp: %s: %w", path, err)
		}
		for name, c := range f.Servers {
			out[name] = c.expanded()
		}
	}
	return out, nil
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
	return c
}

// expand replaces ${VAR} and ${VAR:-default}. Only the braced form:
// a bare $ is a literal, which paths and passwords rely on.
func expand(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "${")
		j := strings.Index(s, "}")
		if i < 0 || j < i {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(lookup(s[i+2 : j]))
		s = s[j+1:]
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
