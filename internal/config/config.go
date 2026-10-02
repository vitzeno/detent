// Package config loads the proposer, Jev judge and loop settings from a
// YAML file, under flags and environment and over built-in defaults. A
// missing file is not an error, but an unreadable -config path is.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/engine"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/sandbox"
)

// Config selects what the proposer and judge talk to, plus loop behavior.
type Config struct {
	BaseURL string            `yaml:"base_url"`
	Model   string            `yaml:"model"`
	APIKey  string            `yaml:"api_key"`
	Headers map[string]string `yaml:"headers"`
	Steps   int               `yaml:"steps"`
	// ContextTokens is how much of the window the transcript may fill before compaction.
	ContextTokens int `yaml:"context_tokens"`
	// CommandTimeout stops a command still running after it, such as "30m".
	// A string because YAML has no duration, and empty takes the built-in.
	CommandTimeout string `yaml:"command_timeout"`

	JevAPIKey     string  `yaml:"jev_api_key"`
	JevModel      string  `yaml:"jev_model"`
	JevEndpoint   string  `yaml:"jev_endpoint"`
	RiskThreshold float64 `yaml:"risk_threshold"`

	// Theme is a name from ui/theme.Themes, checked by main.go so config need not import ui.
	Theme string `yaml:"theme"`

	// LogLevel is "debug", "info", "warn" or "error".
	LogLevel string `yaml:"log_level"`
	// LogBodies lets prompts, replies and command output into the log.
	// Off by default: they carry secrets and bulk.
	LogBodies bool `yaml:"log_bodies"`
	// LogDir holds one JSONL file per session, empty for logging.DefaultDir.
	LogDir string `yaml:"log_dir"`

	// Views is ViewsSaved or ViewsGenerate.
	Views string `yaml:"views"`

	// SandboxMode is "auto" or "host", unvalidated here like Theme.
	SandboxMode string `yaml:"sandbox_mode"`
	// SandboxSocket overrides the OS-conventional containerd socket.
	SandboxSocket  string `yaml:"sandbox_socket"`
	SandboxImage   string `yaml:"sandbox_image"`
	SandboxRuntime string `yaml:"sandbox_runtime"`
	// SandboxNetwork is "host" or "none" (sandbox.Network*). Host means
	// the containerd daemon's host, which on macOS is the colima VM.
	SandboxNetwork   string `yaml:"sandbox_network"`
	SandboxWorkspace string `yaml:"sandbox_workspace"`
}

// Timeout is CommandTimeout as a duration, the built-in when it is empty.
func (c Config) Timeout() (time.Duration, error) {
	if c.CommandTimeout == "" {
		return engine.DefaultCommandTimeout, nil
	}
	d, err := time.ParseDuration(c.CommandTimeout)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("command_timeout %q: want a duration such as 30m", c.CommandTimeout)
	}
	return d, nil
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		BaseURL:       DefaultBaseURL,
		Model:         DefaultModel,
		ContextTokens: DefaultContextTokens,
		JevModel:      classify.DefaultModel,
		RiskThreshold: classify.DefaultRiskThreshold,
		Theme:         DefaultTheme,
		Views:         DefaultViews,
		LogLevel:      DefaultLogLevel,

		SandboxMode:      "auto",
		SandboxImage:     sandbox.DefaultImage,
		SandboxNetwork:   sandbox.NetworkHost,
		SandboxWorkspace: DefaultSandboxWorkspace,
	}
}

// Load reads path, or else the first of ./.detent.y(a)ml and
// ~/.config/detent/config.y(a)ml, or else returns Default.
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

// Defaults: OpenRouter and a large-window model, pinned Jev. Aliased
// from model rather than redeclared, so the two can't silently drift.
const (
	DefaultBaseURL = model.DefaultBaseURL
	DefaultModel   = model.DefaultModel
)

// DefaultContextTokens is aliased for the same reason. 0 means nothing
// here, since an unbounded transcript is what compaction prevents.
const DefaultContextTokens = engine.DefaultContextTokens

// DefaultSandboxWorkspace is the in-container mount point. The host
// source is always the working directory (see sandbox.Container).
const DefaultSandboxWorkspace = "/workspace"

// DefaultTheme mirrors theme.DefaultName, duplicated to avoid the same
// dependency.
const DefaultTheme = "dark"

// The Views modes. There is no "off": the pane draws from a spec either
// way, and the only question is whether a new one may be written.
const (
	// ViewsSaved draws only from specs that exist (built in, shipped
	// for known commands, or saved on disk) and never calls a model.
	ViewsSaved = "saved"
	// ViewsGenerate also asks the judge to compose a spec when nothing
	// covers an output, and saves it so the next run is free.
	ViewsGenerate = "generate"
)

// DefaultViews spends nothing. Generation is opt-in because it costs judge calls.
const DefaultViews = ViewsSaved

// DefaultLogLevel records what happened without recording everything.
const DefaultLogLevel = "info"

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
