package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/config"
)

// -init says where it wrote, and a second run leaves that file alone.
func TestInitConfig_WritesOnceAndSaysWhere(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // where Windows looks for home
	path, err := config.UserPath()
	require.NoError(t, err)

	var out strings.Builder
	require.NoError(t, initConfig(&out))
	assert.Contains(t, out.String(), path)
	assert.Contains(t, out.String(), "api_key")
	require.NoError(t, os.WriteFile(path, []byte("model: mine\n"), 0o600))

	require.ErrorContains(t, initConfig(&out), "already exists")
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "model: mine\n", string(got))
}

// The hint shows only to someone with neither a key nor a config to put one in.
func TestNoConfigHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // where Windows looks for home

	assert.Contains(t, noConfigHint(config.Config{}), "detent -init")
	assert.Empty(t, noConfigHint(config.Config{APIKey: "set"}), "a key from env needs no file")

	var out strings.Builder
	require.NoError(t, initConfig(&out))
	assert.Empty(t, noConfigHint(config.Config{}), "a config exists, it just has no key yet")
}
