package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

const notionURL = "https://mcp.notion.com/mcp"

// A token belongs to a name and a server together: the same name
// pointed somewhere else finds nothing, and its file is another one.
func TestTokens_AreKeyedByTheServerTheyWereIssuedFor(t *testing.T) {
	tokens := Tokens{Dir: filepath.Join(t.TempDir(), "mcp")}
	require.NoError(t, tokens.Save("notion", notionURL, saved()))

	for _, same := range []string{notionURL, "HTTPS://MCP.Notion.com/mcp/", notionURL + "?key=rotated"} {
		got, err := tokens.Load("notion", same)
		require.NoError(t, err)
		assert.NotNil(t, got, same)
	}
	for _, other := range []string{"https://attacker.example/mcp", "https://mcp.notion.com/other", "http://mcp.notion.com/mcp"} {
		got, err := tokens.Load("notion", other)
		require.NoError(t, err)
		assert.Nil(t, got, other)
	}
}

// A file whose recorded server is not the one asked for is not read,
// however it came to be at that path.
func TestTokens_RefuseAFileRecordingAnotherServer(t *testing.T) {
	tokens := Tokens{Dir: filepath.Join(t.TempDir(), "mcp")}
	require.NoError(t, tokens.Save("notion", notionURL, saved()))
	path, err := tokens.path("notion", notionURL)
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(raw), notionURL, "https://elsewhere/mcp", 1)), 0o600))

	got, err := tokens.Load("notion", notionURL)
	require.NoError(t, err)
	assert.Nil(t, got)
}

// A token saved by name alone, as an older detent did, says nothing of
// which server issued it: it is removed, never sent.
func TestTokens_ALegacyNameOnlyTokenIsDropped(t *testing.T) {
	tokens := Tokens{Dir: filepath.Join(t.TempDir(), "mcp")}
	require.NoError(t, os.MkdirAll(tokens.Dir, 0o700))
	legacy := filepath.Join(tokens.Dir, "notion-"+legacySuffix("notion")+".json")
	require.NoError(t, os.WriteFile(legacy, []byte(`{"token":{"access_token":"old"}}`), 0o600))

	require.NoError(t, tokens.dropLegacy("notion"))
	_, err := os.Stat(legacy)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestTokens_RoundTrip(t *testing.T) {
	tokens := Tokens{Dir: filepath.Join(t.TempDir(), "mcp")}
	got, err := tokens.Load("notion", notionURL)
	require.NoError(t, err)
	assert.Nil(t, got, "no sign-in yet is not an error")

	require.NoError(t, tokens.Save("notion", notionURL, saved()))
	got, err = tokens.Load("notion", notionURL)
	require.NoError(t, err)
	assert.Equal(t, "c1", got.ClientID)
	assert.Equal(t, "r", got.Token.RefreshToken)
	assert.True(t, got.Token.Expiry.Equal(saved().Token.Expiry))

	require.NoError(t, tokens.Forget("notion", notionURL))
	got, err = tokens.Load("notion", notionURL)
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.NoError(t, tokens.Forget("notion", notionURL), "forgetting twice is fine")
}

// Only this user may read a token, and the directory holding it.
func TestTokens_AreReadableByTheirOwnerAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no mode bits, a profile is private by ACL")
	}
	tokens := Tokens{Dir: filepath.Join(t.TempDir(), "mcp")}
	require.NoError(t, tokens.Save("notion", notionURL, saved()))
	dir, err := os.Stat(tokens.Dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dir.Mode().Perm())
	path, err := tokens.path("notion", notionURL)
	require.NoError(t, err)
	file, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), file.Mode().Perm())

	require.NoError(t, os.Chmod(path, 0o644))
	_, err = tokens.Load("notion", notionURL)
	assert.ErrorContains(t, err, "readable by others")
}

// A server's name comes from a config file, so it must not choose
// where its token is written, nor land on another server's file.
func TestTokens_NamesCannotLeaveTheDirectory(t *testing.T) {
	root := t.TempDir()
	tokens := Tokens{Dir: filepath.Join(root, "mcp")}
	for _, name := range []string{"../escape", "a/b", "a_b", ""} {
		require.NoError(t, tokens.Save(name, notionURL, saved()))
	}
	entries, err := os.ReadDir(tokens.Dir)
	require.NoError(t, err)
	assert.Len(t, entries, 4, "four names, four files, all inside")
	_, err = os.Stat(filepath.Join(root, "escape.json"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestTokensDir_IsBesideTheEventStore(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".local", "state", "detent", "mcp"), TokensDir())
}

func legacySuffix(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:4])
}

func saved() *Saved {
	return &Saved{ClientID: "c1", AuthURL: "https://as/authorize", TokenURL: "https://as/token",
		Scopes: []string{"read"},
		Token: &oauth2.Token{AccessToken: "a", RefreshToken: "r", TokenType: "Bearer",
			Expiry: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}}
}
