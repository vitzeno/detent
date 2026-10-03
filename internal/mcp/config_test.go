package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shape every MCP client reads, so a file written for one of them
// works here without being rewritten.
func TestLoad_ReadsTheStandardShape(t *testing.T) {
	path := write(t, t.TempDir(), "mcp.json", `{
      "mcpServers": {
        "github": {
          "command": "docker",
          "args": ["run", "-i", "--rm", "ghcr.io/github/github-mcp-server"],
          "env": {"GITHUB_TOKEN": "ghp_x"}
        },
        "linear": {
          "type": "http",
          "url": "https://mcp.linear.app/mcp",
          "headers": {"Authorization": "Bearer t"}
        }
      }
    }`)

	got, err := Load(path)
	require.NoError(t, err)
	require.Len(t, got, 2)

	assert.Equal(t, "docker", got["github"].Command)
	assert.Equal(t, []string{"run", "-i", "--rm", "ghcr.io/github/github-mcp-server"}, got["github"].Args)
	assert.Equal(t, "ghp_x", got["github"].Env["GITHUB_TOKEN"])
	assert.Empty(t, got["github"].Type, "stdio carries no type")

	assert.Equal(t, "http", got["linear"].Type)
	assert.Equal(t, "https://mcp.linear.app/mcp", got["linear"].URL)
	assert.Equal(t, "Bearer t", got["linear"].Headers["Authorization"])
}

// Nearest wins, and a whole entry at a time: half of one server's
// config and half of another's is nobody's intent.
func TestLoad_ProjectReplacesUserWholesale(t *testing.T) {
	dir := t.TempDir()
	user := write(t, dir, "user.json", `{"mcpServers": {
      "shared": {"command": "/usr/bin/old", "args": ["--global"], "env": {"A": "1"}},
      "only-user": {"command": "/usr/bin/keep"}
    }}`)
	project := write(t, dir, "project.json", `{"mcpServers": {
      "shared": {"command": "/usr/bin/new"}
    }}`)

	got, err := Load(user, project)
	require.NoError(t, err)

	assert.Equal(t, "/usr/bin/new", got["shared"].Command)
	assert.Empty(t, got["shared"].Args, "fields were merged across files")
	assert.Empty(t, got["shared"].Env, "fields were merged across files")
	assert.Equal(t, "/usr/bin/keep", got["only-user"].Command, "a user-only server was dropped")
}

// A file safe to commit can still name a token it does not hold.
func TestLoad_ExpandsEnvironmentReferences(t *testing.T) {
	t.Setenv("DETENT_TEST_TOKEN", "secret")
	path := write(t, t.TempDir(), "mcp.json", `{"mcpServers": {"s": {
      "type": "http",
      "url": "${DETENT_TEST_BASE:-https://example.com}/mcp",
      "headers": {"Authorization": "Bearer ${DETENT_TEST_TOKEN}"},
      "command": "",
      "env": {"MISSING": "${DETENT_TEST_ABSENT}"}
    }}}`)

	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/mcp", got["s"].URL, "the default was not used")
	assert.Equal(t, "Bearer secret", got["s"].Headers["Authorization"])
	assert.Empty(t, got["s"].Env["MISSING"], "an unset reference should vanish, not linger")
}

// A bare $ is a literal: paths and passwords rely on it.
func TestExpand_LeavesABareDollarAlone(t *testing.T) {
	t.Setenv("HOME_DIR", "/home/x")
	assert.Equal(t, "pa$$word", expand("pa$$word"))
	assert.Equal(t, "$HOME_DIR", expand("$HOME_DIR"))
	assert.Equal(t, "/home/x", expand("${HOME_DIR}"))
}

// A } before the reference is text, not the end of it: JSON in an
// argument is where that happens.
func TestExpand_ABraceBeforeAReferenceIsText(t *testing.T) {
	t.Setenv("DETENT_TEST_TOKEN", "secret")
	for in, want := range map[string]string{
		`{"a":1}${DETENT_TEST_TOKEN}`: `{"a":1}secret`,
		`a}b${DETENT_TEST_TOKEN}c`:    `a}bsecretc`,
		`${DETENT_TEST_TOKEN`:         `${DETENT_TEST_TOKEN`,
		`x${DETENT_TEST_TOKEN}y}`:     `xsecrety}`,
	} {
		assert.Equal(t, want, expand(in), in)
	}
}

func FuzzExpand(f *testing.F) {
	f.Add("a}b${DETENT_FUZZ}c")
	f.Add("${DETENT_FUZZ:-x}${")
	f.Fuzz(func(t *testing.T, s string) {
		got := expand(s)
		if !strings.Contains(s, "${") {
			assert.Equal(t, s, got, "text with no reference changed")
		}
	})
}

func TestLoad_MissingFilesAreNotAnError(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	require.NoError(t, err)
	assert.Empty(t, got)
}

// A file that is there but broken is worth saying so about, or a
// typo silently costs every server in it.
func TestLoad_BadJSONSaysWhichFile(t *testing.T) {
	path := write(t, t.TempDir(), "mcp.json", `{"mcpServers": {`)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mcp.json")
}

func TestFiles_UserThenProject(t *testing.T) {
	paths := Files()
	require.NotEmpty(t, paths)
	assert.Equal(t, ".mcp.json", paths[len(paths)-1], "the project file has to win")
}

// Absent type means stdio, so type only names a remote transport. One
// we do not speak says so rather than failing somewhere less obvious.
func TestTransport_PicksByTypeAndRefusesTheRest(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  Config
		err  string
	}{
		{"url with no type is http", Config{URL: "https://x/mcp"}, ""},
		{"http", Config{Type: "http", URL: "https://x/mcp"}, ""},
		{"streamable-http", Config{Type: "streamable-http", URL: "https://x/mcp"}, ""},
		{"command with no type is stdio", Config{Command: "/bin/true"}, ""},
		{"explicit stdio", Config{Type: "stdio", Command: "/bin/true"}, ""},

		{"sse is not supported", Config{Type: "sse", URL: "https://x/sse"}, "sse"},
		{"websockets are not supported", Config{Type: "ws", URL: "wss://x"}, "ws"},
		{"a remote type needs a url", Config{Type: "http", Command: "/bin/true"}, "takes a url"},
		{"both transports", Config{Command: "/bin/true", URL: "https://x"}, "not both"},
		{"neither", Config{}, "no command or url"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.cfg.transport(nil)
			if c.err == "" {
				require.NoError(t, err)
				assert.NotNil(t, got)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.err)
		})
	}
}

// Claude Code's own shape, since .mcp.json is its file first. Scopes
// come spaced, as it writes them, or listed, as Gemini CLI does.
func TestLoad_ReadsClaudeCodesOAuth(t *testing.T) {
	t.Setenv("NOTION_SECRET", "s3cret")
	path := write(t, t.TempDir(), "mcp.json", `{"mcpServers": {
      "spaced": {"type": "http", "url": "https://x/mcp", "oauth": {
        "clientId": "id", "clientSecret": "${NOTION_SECRET}", "callbackPort": 8080, "scopes": "read write"}},
      "listed": {"type": "http", "url": "https://y/mcp", "oauth": {"scopes": ["read"]}}
    }}`)
	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, &OAuth{ClientID: "id", ClientSecret: "s3cret", CallbackPort: 8080,
		Scopes: Scopes{"read", "write"}}, got["spaced"].OAuth)
	assert.Equal(t, Scopes{"read"}, got["listed"].OAuth.Scopes)
}

// The usual Notion entry, as written for Claude Code, needs nothing
// added: signing in is the server's to ask for, with a 401.
func TestLoad_AClaudeCodeEntryNeedsNothingAdded(t *testing.T) {
	path := write(t, t.TempDir(), "mcp.json",
		`{"mcpServers": {"notion": {"type": "http", "url": "https://mcp.notion.com/mcp"}}}`)
	got, err := Load(path)
	require.NoError(t, err)
	assert.Nil(t, got["notion"].OAuth, "nothing to tune, and nothing needed")
}

// Cursor keeps a client registered by hand under "auth", spelt its way.
func TestLoad_ReadsCursorsClientCredentials(t *testing.T) {
	path := write(t, t.TempDir(), "mcp.json", `{"mcpServers": {"s": {"url": "https://x/mcp",
      "auth": {"CLIENT_ID": "id", "CLIENT_SECRET": "sec", "scopes": ["read"]}}}}`)
	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, &OAuth{ClientID: "id", ClientSecret: "sec", Scopes: Scopes{"read"}}, got["s"].OAuth)
}

// An "auth" another client wrote is read for what it gives, or left
// alone, so one foreign entry in a shared file never stops detent starting.
func TestLoad_AnotherClientsAuthNeverFailsTheLoad(t *testing.T) {
	for name, auth := range map[string]string{
		"a bare string":       `"oauth"`,
		"an unknown object":   `{"saml": {"x": 1}}`,
		"a number":            `42`,
		"an old nested shape": `{"oauth": {"client_id": "x"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := write(t, t.TempDir(), "mcp.json", `{"mcpServers": {
              "s": {"url": "https://x/mcp", "auth": `+auth+`},
              "other": {"url": "https://y/mcp"}}}`)
			got, err := Load(path)
			require.NoError(t, err)
			assert.Len(t, got, 2, "every other server still loads")
		})
	}
}

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}
