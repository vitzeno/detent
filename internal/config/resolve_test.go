package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/engine"
)

func TestResolve_Precedence(t *testing.T) {
	t.Setenv("DETENT_BASE_URL", "http://env:1/v1")
	t.Setenv("DETENT_MODEL", "env-model")
	t.Setenv("DETENT_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "sk-or-env")
	t.Setenv("OPENAI_API_KEY", "sk-openai-env")

	file := Config{BaseURL: "http://file:2/v1", Model: "file-model", APIKey: "sk-file", Steps: 4}
	got := Resolve(file, Config{BaseURL: "http://flag:3/v1"}, -1)

	assert.Equal(t, "http://flag:3/v1", got.BaseURL) // flag beats env beats file
	assert.Equal(t, "env-model", got.Model)          // env beats file
	assert.Equal(t, "sk-or-env", got.APIKey)         // first set alias wins
	assert.Equal(t, 4, got.Steps)                    // -1 leaves the file value
}

func TestResolve_FlagStepsAndHeaders(t *testing.T) {
	file := Config{
		Headers: map[string]string{"X-Title": "detent"},
		Steps:   9,
	}
	got := Resolve(file, Config{}, 0)
	assert.Equal(t, map[string]string{"X-Title": "detent"}, got.Headers, "file headers must survive")
	assert.Equal(t, 0, got.Steps, "explicit 0 overrides the file value")
	assert.Equal(t, DefaultModel, got.Model)
	assert.Equal(t, DefaultBaseURL, got.BaseURL)
}

func TestResolve_EmptyIsDefault(t *testing.T) {
	t.Setenv("DETENT_BASE_URL", "")
	t.Setenv("DETENT_MODEL", "")
	t.Setenv("DETENT_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("DETENT_THEME", "")
	t.Setenv("DETENT_SANDBOX_MODE", "")
	t.Setenv("DETENT_SANDBOX_SOCKET", "")
	t.Setenv("DETENT_SANDBOX_RUNTIME", "")
	assert.Equal(t, Default(), Resolve(Config{}, Config{}, -1))
}

func TestResolve_JudgePrecedence(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "sk-jev-env")
	file := Config{JevAPIKey: "sk-jev-file", JevModel: "jev-file", RiskThreshold: 0.8}
	got := Resolve(file, Config{}, -1)
	assert.Equal(t, "sk-jev-env", got.JevAPIKey, "env beats file")
	assert.Equal(t, "jev-file", got.JevModel)
	assert.Equal(t, 0.8, got.RiskThreshold)
}

func TestResolve_ThemePrecedence(t *testing.T) {
	t.Setenv("DETENT_THEME", "solarized")
	file := Config{Theme: "light"}

	got := Resolve(file, Config{}, -1)
	assert.Equal(t, "solarized", got.Theme, "env beats file")

	got = Resolve(file, Config{Theme: "dracula"}, -1)
	assert.Equal(t, "dracula", got.Theme, "flag beats env beats file")
}

func TestResolve_SandboxPrecedence(t *testing.T) {
	t.Setenv("DETENT_SANDBOX_MODE", "host")
	t.Setenv("DETENT_SANDBOX_SOCKET", "/env/containerd.sock")
	t.Setenv("DETENT_SANDBOX_RUNTIME", "runsc")
	file := Config{SandboxMode: "auto", SandboxImage: "file-image:latest", SandboxWorkspace: "/file-workspace"}

	got := Resolve(file, Config{}, -1)
	assert.Equal(t, "host", got.SandboxMode, "env beats file")
	assert.Equal(t, "/env/containerd.sock", got.SandboxSocket)
	assert.Equal(t, "runsc", got.SandboxRuntime)
	assert.Equal(t, "file-image:latest", got.SandboxImage, "no env for image; file survives")
	assert.Equal(t, "/file-workspace", got.SandboxWorkspace)

	got = Resolve(file, Config{SandboxMode: "auto"}, -1)
	assert.Equal(t, "auto", got.SandboxMode, "flag beats env beats file")
}

func TestResolve_ContextTokensPrecedence(t *testing.T) {
	t.Setenv("DETENT_CONTEXT_TOKENS", "64000")
	file := Config{ContextTokens: 8_000}

	got := Resolve(file, Config{}, -1)
	assert.Equal(t, 64_000, got.ContextTokens, "env beats file")

	got = Resolve(file, Config{ContextTokens: 12_000}, -1)
	assert.Equal(t, 12_000, got.ContextTokens, "flag beats env beats file")
}

func TestResolve_ContextTokensDefaultsWhenUnset(t *testing.T) {
	t.Setenv("DETENT_CONTEXT_TOKENS", "")
	assert.Equal(t, DefaultContextTokens, Resolve(Config{}, Config{}, -1).ContextTokens)

	// A value that isn't a number sets nothing rather than zeroing the
	// budget, which would mean an unbounded transcript.
	t.Setenv("DETENT_CONTEXT_TOKENS", "lots")
	assert.Equal(t, DefaultContextTokens, Resolve(Config{}, Config{}, -1).ContextTokens)
}

func TestTimeout_ReadsADurationOrTakesTheBuiltIn(t *testing.T) {
	for in, want := range map[string]time.Duration{"": engine.DefaultCommandTimeout, "30m": 30 * time.Minute, "90s": 90 * time.Second} {
		got, err := Config{CommandTimeout: in}.Timeout()
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"10", "soon", "-5m", "0s"} {
		_, err := Config{CommandTimeout: bad}.Timeout()
		assert.Error(t, err, bad)
	}
}

// Unset is on, and either the file or the environment can turn it off.
func TestResolve_FinishCheckIsOnUnlessTurnedOff(t *testing.T) {
	t.Setenv("DETENT_FINISH_CHECK", "")
	assert.True(t, Resolve(Config{}, Config{}, -1).FinishChecks())

	off := false
	assert.False(t, Resolve(Config{FinishCheck: &off}, Config{}, -1).FinishChecks())

	t.Setenv("DETENT_FINISH_CHECK", "false")
	assert.False(t, Resolve(Config{}, Config{}, -1).FinishChecks())
}
