package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "detent.yaml")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func TestLoad_ExplicitFile(t *testing.T) {
	p := writeTemp(t, "base_url: http://x:9999/v1\nmodel: some-model\napi_key: sk-1\nsteps: 12\n")
	cfg, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, "http://x:9999/v1", cfg.BaseURL)
	assert.Equal(t, "some-model", cfg.Model)
	assert.Equal(t, "sk-1", cfg.APIKey)
	assert.Equal(t, 12, cfg.Steps)
}

func TestLoad_PartialFileKeepsDefaults(t *testing.T) {
	p := writeTemp(t, "model: other-model\n")
	cfg, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, "other-model", cfg.Model)
	assert.Equal(t, DefaultBaseURL, cfg.BaseURL)
	assert.Equal(t, DefaultModel, "prism-ml/bonsai-27b")
}

func TestLoad_MissingExplicitPathErrors(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	assert.Error(t, err)
}

func TestLoad_BadYAMLErrors(t *testing.T) {
	p := writeTemp(t, "base_url: [unclosed\n")
	_, err := Load(p)
	assert.Error(t, err)
}

func TestLoad_NoFileReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer func() { _ = os.Chdir(cwd) }()
	t.Setenv("HOME", dir) // empty ~/.config too

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, Default(), cfg)
}

func TestLoad_FindsLocalFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".detent.yaml"), []byte("model: local-model\n"), 0o644))
	cwd, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer func() { _ = os.Chdir(cwd) }()
	t.Setenv("HOME", t.TempDir())

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "local-model", cfg.Model)
}

func TestLoad_Headers(t *testing.T) {
	p := writeTemp(t, "base_url: https://openrouter.ai/api/v1\nmodel: qwen/qwen3-32b\nheaders:\n  HTTP-Referer: https://example.com\n  X-Title: detent\n")
	cfg, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, "https://openrouter.ai/api/v1", cfg.BaseURL)
	assert.Equal(t, map[string]string{"HTTP-Referer": "https://example.com", "X-Title": "detent"}, cfg.Headers)
}

func TestLoad_JudgeFields(t *testing.T) {
	p := writeTemp(t, "jev_api_key: sk-jev\njev_model: jev-9.9.9\njev_endpoint: http://x/v1\nrisk_threshold: 0.7\n")
	cfg, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, "sk-jev", cfg.JevAPIKey)
	assert.Equal(t, "jev-9.9.9", cfg.JevModel)
	assert.Equal(t, "http://x/v1", cfg.JevEndpoint)
	assert.Equal(t, 0.7, cfg.RiskThreshold)
}

func TestLoad_Theme(t *testing.T) {
	p := writeTemp(t, "theme: dracula\n")
	cfg, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, "dracula", cfg.Theme)
}

func TestLoad_SandboxFields(t *testing.T) {
	p := writeTemp(t, "sandbox_mode: host\nsandbox_socket: /tmp/containerd.sock\nsandbox_image: ubuntu:22.04\nsandbox_runtime: runsc\nsandbox_workspace: /work\n")
	cfg, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, "host", cfg.SandboxMode)
	assert.Equal(t, "/tmp/containerd.sock", cfg.SandboxSocket)
	assert.Equal(t, "ubuntu:22.04", cfg.SandboxImage)
	assert.Equal(t, "runsc", cfg.SandboxRuntime)
	assert.Equal(t, "/work", cfg.SandboxWorkspace)
}

func TestLoad_ExampleFileStaysValid(t *testing.T) {
	cwd, _ := os.Getwd()
	require.NoError(t, os.Chdir("../.."))
	defer func() { _ = os.Chdir(cwd) }()
	cfg, err := Load("detent.example.yaml")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:1234/v1", cfg.BaseURL)
	assert.Equal(t, "prism-ml/bonsai-27b", cfg.Model)
}

// Only generate ever writes a spec. saved draws from what already
// exists, which is the distinction the old name ("cached") inverted.
func TestViews_DefaultsToSavedAndSpendsNothing(t *testing.T) {
	assert.Equal(t, ViewsSaved, Default().Views)
	assert.Equal(t, "saved", ViewsSaved)
	assert.NotEqual(t, ViewsGenerate, Default().Views,
		"generation bills the proposer, so it is opt-in")
}
