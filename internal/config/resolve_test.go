package config

import (
	"reflect"
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
	clearEnv(t)
	assert.Equal(t, Default(), Resolve(Config{}, Config{}, -1))
}

// clearEnv blanks every variable envConfig reads, so a developer's own
// DETENT_* cannot change what a test sees.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range envKeys {
		t.Setenv(k, "")
	}
}

// clearEnv is only as good as envKeys, so the two must name the same variables.
func TestEnvConfig_ReadsExactlyEnvKeys(t *testing.T) {
	read := map[string]bool{}
	envConfig(func(k string) string { read[k] = true; return "" })
	want := map[string]bool{}
	for _, k := range envKeys {
		want[k] = true
	}
	assert.Equal(t, want, read)
}

// A field apply forgets is loaded from the file and then silently dropped.
func TestResolve_EveryFieldSurvives(t *testing.T) {
	clearEnv(t)
	for i := range reflect.TypeFor[Config]().NumField() {
		var file Config
		v := reflect.ValueOf(&file).Elem().Field(i)
		name := reflect.TypeFor[Config]().Field(i).Name
		switch v.Kind() {
		case reflect.String:
			v.SetString("set")
		case reflect.Int:
			v.SetInt(7)
		case reflect.Float64:
			v.SetFloat(0.25)
		case reflect.Map:
			v.Set(reflect.ValueOf(map[string]string{"k": "v"}))
		case reflect.Pointer:
			v.Set(reflect.ValueOf(new(true)))
		default:
			t.Fatalf("%s: teach this test about %s", name, v.Kind())
		}
		got := reflect.ValueOf(Resolve(file, Config{}, -1)).Field(i)
		assert.Equal(t, v.Interface(), got.Interface(), "%s was dropped by apply", name)
	}
}

func TestEnvBool_ReadsTheUsualSpellings(t *testing.T) {
	for in, want := range map[string]*bool{
		"true": new(true), "1": new(true), "yes": new(true), "ON": new(true),
		"false": new(false), "0": new(false), "no": new(false), "Off": new(false),
		"": nil, "maybe": nil,
	} {
		assert.Equal(t, want, envBool(in), in)
	}
}

// Bodies carry secrets, so DETENT_LOG_BODIES=false must mean off, even over a file that said on.
func TestResolve_LogBodies(t *testing.T) {
	for _, tc := range []struct {
		name string
		file *bool
		env  string
		want bool
	}{
		{"off by default", nil, "", false},
		{"the file turns it on", new(true), "", true},
		{"env false over a file true", new(true), "false", false},
		{"env 0 over a file true", new(true), "0", false},
		{"env true over a file false", new(false), "true", true},
		{"env yes", nil, "yes", true},
		{"env nonsense leaves the file", new(true), "maybe", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DETENT_LOG_BODIES", tc.env)
			assert.Equal(t, tc.want, Resolve(Config{LogBodies: tc.file}, Config{}, -1).LogsBodies())
		})
	}
}

func TestResolve_JudgePrecedence(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "sk-jev-env")
	file := Config{JevAPIKey: "sk-jev-file", JevModel: "jev-file", RiskThreshold: 0.8}
	got := Resolve(file, Config{}, -1)
	assert.Equal(t, "sk-jev-env", got.JevAPIKey, "env beats file")
	assert.Equal(t, "jev-file", got.JevModel)
	assert.InDelta(t, 0.8, got.RiskThreshold, 1e-9)
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
