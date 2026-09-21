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

	"github.com/vitzeno/detent/internal/agent"
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

// DefaultContextTokens is aliased for the same reason. Unlike Steps, 0
// is not a meaningful value here — an unbounded transcript is the bug
// compaction exists to fix — so a plain default is enough.
const DefaultContextTokens = agent.DefaultContextTokens

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
	// ContextTokens is how much of the model's window the transcript
	// may fill before older turns are summarised away.
	ContextTokens int `yaml:"context_tokens"`

	JevAPIKey     string  `yaml:"jev_api_key"`
	JevModel      string  `yaml:"jev_model"`
	JevEndpoint   string  `yaml:"jev_endpoint"`
	RiskThreshold float64 `yaml:"risk_threshold"`

	// Theme is a name from internal/ui/theme.Themes. Unvalidated here —
	// main.go does the lookup, so config has no dependency on ui.
	Theme string `yaml:"theme"`

	// Views is "off", "saved" or "generate". See the constants for
	// what each one does.
	Views string `yaml:"views"`

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

// How the output pane may draw a command's result.
const (
	// ViewsOff draws with the built-in rendering for the judged kind
	// and nothing else. Exactly how output looked before views existed.
	ViewsOff = "off"
	// ViewsSaved also draws from specs that already exist: the ones
	// detent ships and any saved on disk. It never calls a model, so
	// the set of specs never grows.
	ViewsSaved = "saved"
	// ViewsGenerate does what ViewsSaved does first, and when neither
	// covers an output, asks a model for a spec and saves it. The next
	// run of that command shape is then served from disk for nothing.
	ViewsGenerate = "generate"
)

// DefaultViews draws from what already exists but spends no tokens.
// Generation is opt-in because it bills the proposer.
const DefaultViews = ViewsSaved

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		BaseURL:       DefaultBaseURL,
		Model:         DefaultModel,
		ContextTokens: DefaultContextTokens,
		JevModel:      classify.DefaultModel,
		Theme:         DefaultTheme,
		Views:         DefaultViews,

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
