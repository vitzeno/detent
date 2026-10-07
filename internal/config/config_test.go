package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/model"
)

func TestLoad_ExplicitFile(t *testing.T) {
	p := writeTemp(t, "base_url: http://x:9999/v1\nmodel: some-model\napi_key: sk-1\nsteps: 12\n")
	cfg, err := Load(p, nil)
	require.NoError(t, err)
	assert.Equal(t, "http://x:9999/v1", cfg.BaseURL)
	assert.Equal(t, "some-model", cfg.Model)
	assert.Equal(t, "sk-1", cfg.APIKey)
	assert.Equal(t, 12, cfg.Steps)
}

func TestLoad_PartialFileKeepsDefaults(t *testing.T) {
	p := writeTemp(t, "model: other-model\n")
	cfg, err := Load(p, nil)
	require.NoError(t, err)
	assert.Equal(t, "other-model", cfg.Model)
	assert.Equal(t, DefaultBaseURL, cfg.BaseURL)
}

func TestLoad_MissingExplicitPathErrors(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"), nil)
	assert.Error(t, err)
}

func TestLoad_BadYAMLErrors(t *testing.T) {
	p := writeTemp(t, "base_url: [unclosed\n")
	_, err := Load(p, nil)
	assert.Error(t, err)
}

func TestLoad_EmptyFileIsDefault(t *testing.T) {
	cfg, err := Load(writeTemp(t, ""), nil)
	require.NoError(t, err)
	assert.Equal(t, Default(), cfg)
}

// A misspelt key used to load as nothing at all, which is a setting the user thinks is on.
func TestLoad_UnknownKeyFailsWithItsLine(t *testing.T) {
	_, err := Load(writeTemp(t, "model: m\nsandbox_mod: host\n"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox_mod")
	assert.Contains(t, err.Error(), "line 2")
}

func TestLoad_NoFileReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", dir)                      // empty ~/.config too
	t.Setenv("USERPROFILE", os.Getenv("HOME")) // where Windows looks for home

	cfg, err := Load("", map[string][]byte{})
	require.NoError(t, err)
	assert.Equal(t, Default(), cfg)
}

// The local file is the approved bytes, whatever the disk now holds.
func TestLoad_ReadsLocalFromTheApprovedBytes(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".detent.yaml"), []byte("base_url: http://evil/v1\n"), 0o644))
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME")) // where Windows looks for home

	cfg, err := Load("", map[string][]byte{".detent.yml": []byte("model: local-model\n")})
	require.NoError(t, err)
	assert.Equal(t, "local-model", cfg.Model)
	assert.Equal(t, DefaultBaseURL, cfg.BaseURL)
}

// An untrusted directory's file is passed over for the user's own.
func TestLoad_WithoutLocalSkipsTheWorkingDirectory(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".detent.yaml"), []byte("base_url: http://evil/v1\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".config", "detent"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".config", "detent", "config.yaml"), []byte("model: mine\n"), 0o644))
	t.Chdir(dir)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", os.Getenv("HOME")) // where Windows looks for home

	cfg, err := Load("", nil)
	require.NoError(t, err)
	assert.Equal(t, "mine", cfg.Model)
	assert.Equal(t, DefaultBaseURL, cfg.BaseURL)
}

// A trusted directory's file sets what it names over the human's own. Replacing
// it whole dropped their judge key and mcp_trust_hints: false unseen.
func TestLoad_LayersLocalOverTheUsersOwn(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".config", "detent"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".config", "detent", "config.yaml"),
		[]byte("model: mine\njev_api_key: my-key\nmcp_trust_hints: false\n"), 0o644))
	t.Chdir(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", os.Getenv("HOME")) // where Windows looks for home

	cfg, err := Load("", map[string][]byte{".detent.yaml": []byte("model: local-model\n")})
	require.NoError(t, err)
	assert.Equal(t, "local-model", cfg.Model)
	assert.Equal(t, "my-key", cfg.JevAPIKey)
	assert.False(t, cfg.TrustsMCPHints())
}

func TestLoad_Headers(t *testing.T) {
	p := writeTemp(t, "base_url: https://openrouter.ai/api/v1\nmodel: qwen/qwen3-32b\nheaders:\n  HTTP-Referer: https://example.com\n  X-Title: detent\n")
	cfg, err := Load(p, nil)
	require.NoError(t, err)
	assert.Equal(t, "https://openrouter.ai/api/v1", cfg.BaseURL)
	assert.Equal(t, map[string]string{"HTTP-Referer": "https://example.com", "X-Title": "detent"}, cfg.Headers)
}

func TestLoad_JudgeFields(t *testing.T) {
	p := writeTemp(t, "jev_api_key: sk-jev\njev_model: jev-9.9.9\njev_endpoint: http://x/v1\nrisk_threshold: 0.7\n")
	cfg, err := Load(p, nil)
	require.NoError(t, err)
	assert.Equal(t, "sk-jev", cfg.JevAPIKey)
	assert.Equal(t, "jev-9.9.9", cfg.JevModel)
	assert.Equal(t, "http://x/v1", cfg.JevEndpoint)
	assert.InDelta(t, 0.7, cfg.RiskThreshold, 1e-9)
}

func TestLoad_Theme(t *testing.T) {
	p := writeTemp(t, "theme: dracula\n")
	cfg, err := Load(p, nil)
	require.NoError(t, err)
	assert.Equal(t, "dracula", cfg.Theme)
}

func TestLoad_SandboxFields(t *testing.T) {
	p := writeTemp(t, "sandbox_mode: host\nsandbox_socket: /tmp/containerd.sock\nsandbox_image: ubuntu:22.04\nsandbox_runtime: runsc\nsandbox_workspace: /work\n")
	cfg, err := Load(p, nil)
	require.NoError(t, err)
	assert.Equal(t, "host", cfg.SandboxMode)
	assert.Equal(t, "/tmp/containerd.sock", cfg.SandboxSocket)
	assert.Equal(t, "ubuntu:22.04", cfg.SandboxImage)
	assert.Equal(t, "runsc", cfg.SandboxRuntime)
	assert.Equal(t, "/work", cfg.SandboxWorkspace)
}

// Pins the alias rather than the value, which only a deliberate change
// would break.
func TestDefaults_DoNotDriftFromModel(t *testing.T) {
	assert.Equal(t, model.DefaultModel, DefaultModel)
	assert.Equal(t, model.DefaultBaseURL, DefaultBaseURL)
}

// Only generate ever writes a spec. saved draws from what already exists.
func TestViews_DefaultsToSavedAndSpendsNothing(t *testing.T) {
	assert.Equal(t, ViewsSaved, Default().Views)
	assert.Equal(t, "saved", ViewsSaved)
	assert.NotEqual(t, ViewsGenerate, Default().Views,
		"generation costs judge calls, so it is opt-in")
}

func TestValidate_ReportsEveryBadValue(t *testing.T) {
	require.NoError(t, Default().Validate())

	bad := Default()
	bad.SandboxMode = "docker"
	bad.SandboxNetwork = "bridge"
	bad.Views = "generte"
	bad.LogLevel = "verbose"
	bad.RiskThreshold = 1.5
	bad.Steps = -1
	bad.ContextTokens = -1
	bad.CommandTimeout = "soon"
	bad.HostShell = "cmd"
	err := bad.Validate()
	require.Error(t, err)
	for _, want := range []string{"docker", "bridge", "generte", "verbose", "1.5", "steps", "context_tokens", "soon", `host_shell "cmd"`} {
		assert.Contains(t, err.Error(), want)
	}

	gen := Default()
	gen.Views = ViewsGenerate
	require.ErrorContains(t, gen.Validate(), "jev_api_key")
	gen.JevAPIKey = "k"
	assert.NoError(t, gen.Validate())
}

func TestValidate_TheSandboxIsRefusedOnWindows(t *testing.T) {
	auto := Default()
	auto.SandboxMode = SandboxAuto
	require.ErrorContains(t, auto.validate("windows"), "use sandbox_mode host")
	require.NoError(t, auto.validate("linux"))
	require.NoError(t, Default().validate("windows"), "host mode is fine")
}

func TestValidate_TakesEveryHostShell(t *testing.T) {
	for _, name := range []string{"", "sh", "pwsh", "gitbash"} {
		c := Default()
		c.HostShell = name
		assert.NoError(t, c.Validate(), name)
	}
}

func TestLogValue_MasksTheKeys(t *testing.T) {
	c := Config{APIKey: "sk-secret", JevAPIKey: "sk-jev", Headers: map[string]string{"Authorization": "Bearer sk-h"}, Model: "m"}
	got := c.LogValue().String()
	assert.NotContains(t, got, "sk-")
	assert.Contains(t, got, "m")
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "detent.yaml")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}
