// Package config loads every knob from a YAML file: the proposer
// (any OpenAI-compatible endpoint: LM Studio, OpenRouter, OpenAI),
// the Jev judge, and loop behavior. Precedence is flags, then
// environment, then file, then built-in defaults. A missing file is
// not an error; an explicit -config path that can't be read is.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/sandbox"
)

// Defaults: local LM Studio server, bonsai for now, pinned Jev. Aliased
// from propose rather than redeclared, so the two can't silently drift.
const (
	DefaultBaseURL = propose.DefaultBaseURL
	DefaultModel   = propose.DefaultModel
)

// DefaultSandboxWorkspace is the in-container mount point, not the
// host source (always os.Getwd(); see sandbox.Container).
const DefaultSandboxWorkspace = "/workspace"

// Config selects what the proposer and judge talk to, plus loop behavior.
type Config struct {
	BaseURL string            `yaml:"base_url"`
	Model   string            `yaml:"model"`
	APIKey  string            `yaml:"api_key"`
	Headers map[string]string `yaml:"headers"`
	Steps   int               `yaml:"steps"`

	JevAPIKey     string  `yaml:"jev_api_key"`
	JevModel      string  `yaml:"jev_model"`
	JevEndpoint   string  `yaml:"jev_endpoint"`
	RiskThreshold float64 `yaml:"risk_threshold"`

	// Theme is a name from internal/ui/theme.Themes. Unvalidated here —
	// main.go does the lookup, so config has no dependency on ui.
	Theme string `yaml:"theme"`

	// SandboxMode is "auto" or "host"; unvalidated here, like Theme.
	SandboxMode string `yaml:"sandbox_mode"`
	// SandboxSocket overrides the OS-conventional containerd socket
	// path; empty lets main.go's defaultSandboxSocket() pick it.
	SandboxSocket  string `yaml:"sandbox_socket"`
	SandboxImage   string `yaml:"sandbox_image"`
	SandboxRuntime string `yaml:"sandbox_runtime"`
	// SandboxNetwork is "host" or "none" (sandbox.Network*). Host means
	// the containerd daemon's host, which on macOS is the colima VM.
	SandboxNetwork   string `yaml:"sandbox_network"`
	SandboxWorkspace string `yaml:"sandbox_workspace"`
}

// DefaultTheme mirrors theme.DefaultName, duplicated to avoid the same
// dependency.
const DefaultTheme = "dark"

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		BaseURL:  DefaultBaseURL,
		Model:    DefaultModel,
		JevModel: classify.DefaultModel,
		Theme:    DefaultTheme,

		SandboxMode:      "auto",
		SandboxImage:     sandbox.DefaultImage,
		SandboxNetwork:   sandbox.NetworkHost,
		SandboxWorkspace: DefaultSandboxWorkspace,
	}
}

// Load reads path, or searches the standard locations when path is
// empty: ./.detent.yaml (or .yml), then ~/.config/detent/config.yaml
// (or .yml). No file anywhere returns Default with no error.
func Load(path string) (Config, error) {
	if path != "" {
		return read(path)
	}
	candidates := []string{".detent.yaml", ".detent.yml"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".config", "detent", "config.yaml"),
			filepath.Join(home, ".config", "detent", "config.yml"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return read(c)
		}
	}
	return Default(), nil
}

func read(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	cfg := Default()
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}
