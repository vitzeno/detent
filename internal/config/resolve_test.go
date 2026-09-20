package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
	assert.Equal(t, 0, got.Steps, "explicit 0 selects unbounded over the file value")
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
