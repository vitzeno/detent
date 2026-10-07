package trust

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// Summary says what the hashed bytes would do, and never a secret: keys are
// "set", headers and variables are named, and URLs lose credentials and query.
func Summary(dir string, present []string, files map[string][]byte, changed bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "detent: %s has its own configuration. It can change where your API key goes,\n", printable(dir))
	b.WriteString("turn off the sandbox and start programs on this machine.\n")
	for _, name := range present {
		raw := files[name]
		fmt.Fprintf(&b, "  %s\n", name)
		var lines []string
		switch name {
		case ".env":
			lines = dotenv(raw)
		case ".mcp.json":
			lines = servers(raw)
		default:
			lines = detentYAML(raw)
		}
		for _, l := range lines {
			fmt.Fprintf(&b, "    %s\n", printable(l))
		}
	}
	if changed {
		b.WriteString("  These files changed since you last trusted this directory.\n")
	}
	return b.String()
}

func detentYAML(raw []byte) []string {
	var m map[string]any
	// The error is not printed, since a decode error can quote a value.
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return []string{"not valid YAML"}
	}
	var out, other []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		v := m[k]
		switch {
		case k == "base_url" || k == "jev_endpoint":
			out = append(out, fmt.Sprintf("%s: %s", k, cleanURL(fmt.Sprint(v))))
		case k == "model", k == "host_shell", strings.HasPrefix(k, "sandbox_"), strings.HasPrefix(k, "log_"):
			out = append(out, fmt.Sprintf("%s: %v", k, v))
		case k == "api_key" || k == "jev_api_key":
			out = append(out, k+": set")
		case k == "headers":
			h, _ := v.(map[string]any)
			out = append(out, "headers: "+strings.Join(slices.Sorted(maps.Keys(h)), ", "))
		default:
			other = append(other, k)
		}
	}
	if len(other) > 0 {
		out = append(out, "also sets: "+strings.Join(other, ", "))
	}
	return out
}

func dotenv(raw []byte) []string {
	var names []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if key = strings.TrimSpace(key); ok && key != "" {
			names = append(names, key)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return []string{"sets: " + strings.Join(names, ", ")}
}

// server is the part of an .mcp.json entry worth showing.
type server struct {
	Command  string            `json:"command"`
	Args     []string          `json:"args"`
	URL      string            `json:"url"`
	Env      map[string]string `json:"env"`
	Headers  map[string]string `json:"headers"`
	Disabled bool              `json:"disabled"`
	OAuth    json.RawMessage   `json:"oauth"`
	Auth     json.RawMessage   `json:"auth"`
}

func servers(raw []byte) []string {
	var f struct {
		Servers map[string]server `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return []string{"not valid JSON"}
	}
	var out []string
	for _, name := range slices.Sorted(maps.Keys(f.Servers)) {
		s := f.Servers[name]
		// Both are shown when both are set, since expanding can empty either.
		var what []string
		if s.URL != "" {
			what = append(what, cleanURL(s.URL))
		}
		if s.Command != "" || s.URL == "" {
			what = append(what, "runs "+strings.Join(append([]string{s.Command}, s.Args...), " "))
		}
		line := name + ": " + strings.Join(what, " or ")
		if s.Disabled {
			line += " (disabled)"
		}
		out = append(out, line)
		if len(s.Env) > 0 {
			out = append(out, "  env: "+strings.Join(slices.Sorted(maps.Keys(s.Env)), ", "))
		}
		if len(s.Headers) > 0 {
			out = append(out, "  headers: "+strings.Join(slices.Sorted(maps.Keys(s.Headers)), ", "))
		}
		if refs := references(s); len(refs) > 0 {
			out = append(out, "  reads your environment: "+strings.Join(refs, ", "))
		}
	}
	return out
}

var reference = regexp.MustCompile(`\$\{([^}:]+)`)

// references names every ${VAR} a server expands, since that is how a
// committed file reaches a secret it does not hold.
func references(s server) []string {
	all := []string{s.Command, s.URL, string(s.OAuth), string(s.Auth)}
	all = append(all, s.Args...)
	for _, v := range s.Env {
		all = append(all, v)
	}
	for _, v := range s.Headers {
		all = append(all, v)
	}
	var names []string
	for _, v := range all {
		for _, m := range reference.FindAllStringSubmatch(v, -1) {
			if !slices.Contains(names, m[1]) {
				names = append(names, m[1])
			}
		}
	}
	slices.Sort(names)
	return names
}

// cleanURL drops user info, query and fragment, any of which can carry a token.
func cleanURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(not a URL)"
	}
	query := u.RawQuery != ""
	u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
	if query {
		return u.String() + "?…"
	}
	return u.String()
}

// printable keeps a file from moving the cursor or recolouring the question.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, s)
}
