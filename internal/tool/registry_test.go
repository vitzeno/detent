package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// A bad call must come back as something the model can
// read and correct, never a Go error that ends the Turn.
func TestPrepare_BadToolCallsExplainThemselves(t *testing.T) {
	r := Standard()
	tests := []struct {
		name  string
		tool  event.ToolName
		args  map[string]any
		wants []string // substrings the message must carry
	}{
		{
			name:  "unregistered tool lists what exists",
			tool:  "delete_file",
			args:  map[string]any{"path": "x"},
			wants: []string{"no tool named", "delete_file", "bash", "read_file"},
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
		tool event.ToolName
		args map[string]any
		want string
		mut  string
	}{
		{"bash passes through", "bash", map[string]any{"command": "git status"}, "git status", ""},
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

// A path the model chose is untrusted input that reaches `sh -c`, so
// every tool taking one runs it here and nothing it names may execute.
func TestPrepare_QuotesHostilePaths(t *testing.T) {
	r := Standard()
	for _, p := range []string{
		"a b.go", "$(touch pwned)", "`touch pwned`", "a'; touch pwned; '", "--; touch pwned", "*", "year=2024/x", "!",
	} {
		for _, call := range []struct {
			tool event.ToolName
			args map[string]any
		}{
			{"read_file", map[string]any{"path": p}},
			{"write_file", map[string]any{"path": p, "content": "x\n"}},
			{"edit_file", map[string]any{"path": p, "old_string": "x", "new_string": "y"}},
			{"list_dir", map[string]any{"path": p}},
			{"grep", map[string]any{"pattern": "x", "path": p}},
			{"find_files", map[string]any{"pattern": "*", "path": p}},
		} {
			dir := t.TempDir()
			c, err := r.Prepare(call.tool, call.args)
			require.NoError(t, err)
			_, _ = shIn(t, dir, c.Command)
			_, err = os.Stat(filepath.Join(dir, "pwned"))
			assert.True(t, os.IsNotExist(err), "%s ran part of %q", call.tool, p)
		}
	}
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
		tl, ok := r.Lookup(event.ToolName(name))
		require.True(t, ok)

		assert.NotEmpty(t, fn["description"], "%s has no description", name)
		params := fn["parameters"].(map[string]any)
		props := params["properties"].(map[string]any)
		assert.Len(t, props, len(tl.Describe().Params))
		assert.Equal(t, false, params["additionalProperties"])

		// Strict mode requires every property in required, or an endpoint
		// rejects the request, so an optional parameter is nullable instead.
		assert.Len(t, params["required"], len(tl.Describe().Params))
		for _, p := range tl.Describe().Params {
			got, ok := props[p.Name].(map[string]any)
			require.True(t, ok, "%s.%s missing from schema", name, p.Name)
			assert.NotEmpty(t, got["description"], "%s.%s has no description", name, p.Name)
			if p.Required {
				assert.Equal(t, p.Type, got["type"])
			} else {
				assert.Equal(t, []string{p.Type, "null"}, got["type"], "%s.%s is optional", name, p.Name)
			}
		}
	}
}

// An explicit null is how strict mode sends an absent optional.
// A subagent's registry keeps the tools it is given, in the order the
// model would see them, and nothing else.
func TestRegistry_OnlyKeepsTheNamedToolsInOrder(t *testing.T) {
	reg := Standard(SpawnAgent{})
	sub := reg.Only("grep", "read_file", "no_such_tool")
	assert.Equal(t, []event.ToolName{"read_file", "grep"}, sub.Names())
	_, ok := sub.Lookup(event.ToolSpawnAgent)
	assert.False(t, ok, "a child must not be able to spawn")
	assert.Error(t, sub.Register(ReadFile{}), "a kept built-in is still a built-in")
}

func TestRegistry_WithoutDropsOnlyTheNamedTools(t *testing.T) {
	reg := Standard(SpawnAgent{})
	sub := reg.Without(event.ToolSpawnAgent)
	assert.Equal(t, slices.DeleteFunc(reg.Names(), func(n event.ToolName) bool { return n == event.ToolSpawnAgent }), sub.Names())
}

func TestPrepare_TreatsNullAsAbsent(t *testing.T) {
	c, err := Standard().Prepare("read_file", map[string]any{"path": "a.go", "max_lines": nil})
	require.NoError(t, err)
	assert.Contains(t, c.Command, "-v s=1 -v n=500 ", "null means take the default")
}

func TestRegistry_RegisterOverridesWithoutDuplicating(t *testing.T) {
	r := Standard()
	require.NoError(t, r.Register(fakeMCP{"srv__x"}))
	before := len(r.Names())
	require.NoError(t, r.Register(fakeMCP{"srv__x"}))
	assert.Len(t, r.Names(), before, "re-registering a name must replace, not append")
}

// Nothing a server publishes may shadow bash or any other built-in.
func TestRegistry_ABuiltInWinsACollision(t *testing.T) {
	r := Standard(NewSkill(nil))
	for _, name := range []event.ToolName{"bash", "read_file", "skill"} {
		require.Error(t, r.Register(fakeMCP{name}), name)
		got, ok := r.Lookup(name)
		require.True(t, ok)
		assert.NotEqual(t, "mcp", got.Describe().Executor, "%s was replaced", name)
	}
	r.Unregister("bash")
	_, ok := r.Lookup("bash")
	assert.True(t, ok, "a built-in cannot be unregistered either")
}

// The built-ins keep the order Standard gives them, and only the rest is sorted.
func TestRegistry_KeepsBuiltInsInTheirOwnOrder(t *testing.T) {
	r := Standard(NewSkill(nil))
	require.NoError(t, r.Register(fakeMCP{"zz__last"}))
	require.NoError(t, r.Register(fakeMCP{"aa__first"}))
	assert.Equal(t, []event.ToolName{
		"bash", "read_file", "write_file", "edit_file", "list_dir", "grep", "find_files", "web_search", "skill",
		"aa__first", "zz__last",
	}, r.Names())
}

// PowerShell takes bash's place, so the model is never offered a shell it is not in.
func TestStandardFor_PutsTheShellInBashsPlace(t *testing.T) {
	r := StandardFor(PowerShell{}, NewSkill(nil))
	names := r.Names()
	assert.Equal(t, event.ToolPowerShell, names[0])
	assert.NotContains(t, names, event.ToolBash)
	assert.Len(t, names, len(Standard(NewSkill(nil)).Names()))

	call, err := r.Prepare(event.ToolPowerShell, map[string]any{"command": "Get-ChildItem"})
	require.NoError(t, err)
	assert.Equal(t, "Get-ChildItem", call.Command)
	assert.Empty(t, call.Mutability, "unknown, as bash's is")
	_, err = r.Prepare(event.ToolPowerShell, map[string]any{"command": ""})
	require.Error(t, err)

	// The approval box shows the command itself, which event spells by name.
	assert.Equal(t, "Get-ChildItem", event.Command(event.ToolPowerShell, map[string]any{"command": "Get-ChildItem"}))
	assert.Contains(t, PowerShell{}.Describe().Description, "not a POSIX shell")
}

// A server dialled again takes its old tools with it, so the model is
// never offered one whose session is closed.
func TestRegistry_UnregisterRemovesFromWhatTheModelSees(t *testing.T) {
	r := Standard()
	require.NoError(t, r.Register(fakeMCP{"srv__x"}))
	before := len(r.Names())
	r.Unregister("srv__x", "no-such-tool")
	_, ok := r.Lookup("srv__x")
	assert.False(t, ok)
	assert.Len(t, r.Names(), before-1)
	assert.NotContains(t, r.Names(), event.ToolName("srv__x"))
}

func TestShell_IsNamedForWhereCommandsRun(t *testing.T) {
	assert.Equal(t, event.ToolBash, Shell(false).Name())
	assert.Equal(t, event.ToolPowerShell, Shell(true).Name())
}
