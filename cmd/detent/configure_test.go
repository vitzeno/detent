package main

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcppkg "github.com/vitzeno/detent/internal/mcp"
	"github.com/vitzeno/detent/internal/trust"
)

func TestDotenvLine_ReadsWhatDotenvFilesHold(t *testing.T) {
	for line, want := range map[string][2]string{
		"KEY=v":                   {"KEY", "v"},
		"  KEY = v  ":             {"KEY", "v"},
		"export KEY=v":            {"KEY", "v"},
		`KEY="a b"`:               {"KEY", "a b"},
		`KEY='a # not a comment'`: {"KEY", "a # not a comment"},
		`KEY='abc"`:               {"KEY", `'abc"`},
		`KEY=ends in quote"`:      {"KEY", `ends in quote"`},
		"KEY=v # a comment":       {"KEY", "v"},
		"KEY=a#b":                 {"KEY", "a#b"},
		"KEY=":                    {"KEY", ""},
		"KEY=x=y":                 {"KEY", "x=y"},
	} {
		key, value, ok := dotenvLine(line)
		require.True(t, ok, line)
		assert.Equal(t, want, [2]string{key, value}, line)
	}
	for _, line := range []string{"", "  ", "# KEY=v", "no equals", "=v"} {
		_, _, ok := dotenvLine(line)
		assert.False(t, ok, line)
	}
}

// A real variable wins, even one set to "".
func TestLoadDotenv_FillsOnlyGaps(t *testing.T) {
	raw := []byte("DETENT_T_SET=file\nexport DETENT_T_EMPTY=file\nDETENT_T_NEW='file'\n")
	t.Setenv("DETENT_T_SET", "real")
	t.Setenv("DETENT_T_EMPTY", "")
	t.Setenv("DETENT_T_NEW", "")
	require.NoError(t, os.Unsetenv("DETENT_T_NEW"))

	require.NoError(t, loadDotenv(raw))
	assert.Equal(t, "real", os.Getenv("DETENT_T_SET"))
	assert.Empty(t, os.Getenv("DETENT_T_EMPTY"))
	assert.Equal(t, "file", os.Getenv("DETENT_T_NEW"))

	assert.NoError(t, loadDotenv(nil), "no .env is not an error")
}

// What was approved is what loads, even if the files change after the question.
func TestLayer_LoadsWhatTrustApproved(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DETENT_MODEL", "")
	require.NoError(t, os.Unsetenv("DETENT_MODEL"))
	t.Setenv("DETENT_T_APPROVED", "")
	require.NoError(t, os.Unsetenv("DETENT_T_APPROVED"))
	files := map[string]string{
		".detent.yaml": "model: approved\n",
		".env":         "DETENT_T_APPROVED=yes\n",
		".mcp.json":    `{"mcpServers": {"approved": {"command": "true"}}}`,
	}
	for name, body := range files {
		require.NoError(t, os.WriteFile(name, []byte(body), 0o600))
	}
	var asked bool
	trusted, err := trust.Decide(trust.Options{Dir: dir, State: t.TempDir(),
		Ask: func() bool { asked = true; return true }, Out: io.Discard})
	require.NoError(t, err)
	require.True(t, asked)

	require.NoError(t, os.WriteFile(".detent.yaml", []byte("model: swapped\n"), 0o600))
	require.NoError(t, os.WriteFile(".env", []byte("DETENT_T_APPROVED=swapped\n"), 0o600))
	require.NoError(t, os.WriteFile(".mcp.json", []byte(`{"mcpServers": {"swapped": {"command": "sh"}}}`), 0o600))

	cfg, err := layer(options{steps: -1}, trusted)
	require.NoError(t, err)
	assert.Equal(t, "approved", cfg.Model)
	assert.Equal(t, "yes", os.Getenv("DETENT_T_APPROVED"))
	servers, err := loadMCPConfig(true, trusted.Files[mcppkg.Project], nil)
	require.NoError(t, err)
	assert.Contains(t, servers, "approved")
	assert.NotContains(t, servers, "swapped")
}
