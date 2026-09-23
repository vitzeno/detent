package tool

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The phase gate: a bad call must come back as something the model can
// read and correct, never a Go error that ends the Turn.
func TestPrepare_BadCallsExplainThemselves(t *testing.T) {
	r := Standard()
	tests := []struct {
		name  string
		tool  string
		args  map[string]any
		wants []string // substrings the message must carry
	}{
		{
			name:  "unregistered tool lists what exists",
			tool:  "edit_file",
			args:  map[string]any{"path": "x"},
			wants: []string{"no tool named", "edit_file", "bash", "read_file"},
		},
		{
			name:  "missing required parameter names it",
			tool:  "read_file",
			args:  map[string]any{},
			wants: []string{"read_file", "missing required", "path"},
		},
		{
			name:  "wrong type says which and what was sent",
			tool:  "read_file",
			args:  map[string]any{"path": 42},
			wants: []string{"path", "string", "int"},
		},
		{
			name:  "hallucinated parameter lists the real ones",
			tool:  "read_file",
			args:  map[string]any{"path": "x", "encoding": "utf8"},
			wants: []string{"unknown parameter", "encoding", "max_lines", "path"},
		},
		{
			name:  "empty required string is caught by the tool",
			tool:  "bash",
			args:  map[string]any{"command": ""},
			wants: []string{"bash", "empty"},
		},
		{
			name:  "nonsense that parses is still refused",
			tool:  "read_file",
			args:  map[string]any{"path": "x", "max_lines": float64(0)},
			wants: []string{"max_lines", "positive"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Prepare(tt.tool, tt.args)
			require.Error(t, err)
			for _, w := range tt.wants {
				assert.Contains(t, err.Error(), w)
			}
		})
	}
}

func TestPrepare_LowersToCommands(t *testing.T) {
	r := Standard()
	tests := []struct {
		name string
		tool string
		args map[string]any
		want string
		mut  string
	}{
		{"bash passes through", "bash", map[string]any{"command": "git status"}, "git status", ""},
		{"read_file defaults its limit", "read_file", map[string]any{"path": "a.go"},
			"head -n 500 -- 'a.go'", "read_only"},
		{"read_file takes a limit", "read_file", map[string]any{"path": "a.go", "max_lines": float64(10)},
			"head -n 10 -- 'a.go'", "read_only"},
		{"list_dir defaults to cwd", "list_dir", map[string]any{}, "ls -l -- '.'", "read_only"},
		{"list_dir takes all", "list_dir", map[string]any{"path": "/tmp", "all": true},
			"ls -la -- '/tmp'", "read_only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := r.Prepare(tt.tool, tt.args)
			require.NoError(t, err)
			assert.Equal(t, tt.want, c.Command)
			assert.Equal(t, tt.mut, c.Mutability)
		})
	}
}

// A path the model chose is untrusted input that reaches `sh -c`.
func TestPrepare_QuotesHostilePaths(t *testing.T) {
	r := Standard()
	for _, path := range []string{
		"a b.go", "$(rm -rf /)", "`whoami`", "a'; rm -rf /; '", "--; rm -rf /", "*",
	} {
		c, err := r.Prepare("read_file", map[string]any{"path": path})
		require.NoError(t, err)
		assert.Contains(t, c.Command, quote(path), "%q must reach the shell as one literal", path)
		assert.Equal(t, 1, countUnescapedQuotes(c.Command)%2+1, "quotes must balance: %s", c.Command)
	}
}

func countUnescapedQuotes(s string) int {
	n := 0
	for _, r := range s {
		if r == '\'' {
			n++
		}
	}
	return n
}

// Registering a tool is meant to be the whole job, so the schema the
// model sees has to come from the same Spec validation reads.
func TestSchemas_MatchTheSpecs(t *testing.T) {
	r := Standard()
	schemas := r.Schemas()
	require.Len(t, schemas, len(r.Names()))

	raw, err := json.Marshal(schemas)
	require.NoError(t, err, "schemas must survive the wire")
	assert.Contains(t, string(raw), `"strict":true`)

	for _, s := range schemas {
		fn := s["function"].(map[string]any)
		name := fn["name"].(string)
		tl, ok := r.Lookup(name)
		require.True(t, ok)

		assert.NotEmpty(t, fn["description"], "%s has no description", name)
		params := fn["parameters"].(map[string]any)
		props := params["properties"].(map[string]any)
		assert.Len(t, props, len(tl.Describe().Params))
		assert.Equal(t, false, params["additionalProperties"])

		for _, p := range tl.Describe().Params {
			got, ok := props[p.Name].(map[string]any)
			require.True(t, ok, "%s.%s missing from schema", name, p.Name)
			assert.Equal(t, p.Type, got["type"])
			assert.NotEmpty(t, got["description"], "%s.%s has no description", name, p.Name)
		}
	}
}

func TestRegistry_RegisterOverridesWithoutDuplicating(t *testing.T) {
	r := Standard()
	before := len(r.Names())
	r.Register(Bash{})
	assert.Len(t, r.Names(), before, "re-registering a name must replace, not append")
}
