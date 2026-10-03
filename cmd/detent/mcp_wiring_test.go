package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadMCPConfig_SkipsFilesForHeadlessRuns(t *testing.T) {
	broken := filepath.Join(t.TempDir(), ".mcp.json")
	require.NoError(t, os.WriteFile(broken, []byte("not json"), 0o600))

	got, err := loadMCPConfig(false, []byte("not json"), []string{broken})
	require.NoError(t, err, "headless run must not read MCP config")
	require.Empty(t, got)
}

func TestLoadMCPConfig_LoadsFilesForTUI(t *testing.T) {
	broken := filepath.Join(t.TempDir(), ".mcp.json")
	require.NoError(t, os.WriteFile(broken, []byte("not json"), 0o600))

	_, err := loadMCPConfig(true, nil, []string{broken})
	require.Error(t, err, "TUI should retain normal MCP config validation")
}
