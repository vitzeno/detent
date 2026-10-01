package mcp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func saved() *Saved {
	return &Saved{ClientID: "c1", AuthURL: "https://as/authorize", TokenURL: "https://as/token",
		Scopes: []string{"read"},
		Token: &oauth2.Token{AccessToken: "a", RefreshToken: "r", TokenType: "Bearer",
			Expiry: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}}
}

func TestTokens_RoundTrip(t *testing.T) {
	tokens := Tokens{Dir: filepath.Join(t.TempDir(), "mcp")}
	got, err := tokens.Load("notion")
	require.NoError(t, err)
	assert.Nil(t, got, "no sign-in yet is not an error")

	require.NoError(t, tokens.Save("notion", saved()))
	got, err = tokens.Load("notion")
	require.NoError(t, err)
	assert.Equal(t, "c1", got.ClientID)
	assert.Equal(t, "r", got.Token.RefreshToken)
	assert.True(t, got.Token.Expiry.Equal(saved().Token.Expiry))

	require.NoError(t, tokens.Forget("notion"))
	got, err = tokens.Load("notion")
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.NoError(t, tokens.Forget("notion"), "forgetting twice is fine")
}

// Only this user may read a token, and the directory holding it.
func TestTokens_AreReadableByTheirOwnerAlone(t *testing.T) {
	tokens := Tokens{Dir: filepath.Join(t.TempDir(), "mcp")}
	require.NoError(t, tokens.Save("notion", saved()))
	dir, err := os.Stat(tokens.Dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dir.Mode().Perm())
	path, err := tokens.path("notion")
	require.NoError(t, err)
	file, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), file.Mode().Perm())

	require.NoError(t, os.Chmod(path, 0o644))
	_, err = tokens.Load("notion")
	assert.ErrorContains(t, err, "readable by others")
}

// A server's name comes from a config file, so it must not choose
// where its token is written, nor land on another server's file.
func TestTokens_NamesCannotLeaveTheDirectory(t *testing.T) {
	root := t.TempDir()
	tokens := Tokens{Dir: filepath.Join(root, "mcp")}
	for _, name := range []string{"../escape", "a/b", "a_b", ""} {
		require.NoError(t, tokens.Save(name, saved()))
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
