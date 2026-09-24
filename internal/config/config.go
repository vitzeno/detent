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
	"github.com/vitzeno/detent/internal/engine"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/sandbox"
)

// Defaults: local LM Studio server, bonsai for now, pinned Jev. Aliased
// from propose rather than redeclared, so the two can't silently drift.
const (
	DefaultBaseURL = model.DefaultBaseURL
	DefaultModel   = model.DefaultModel
)

// DefaultContextTokens is aliased for the same reason. Unlike Steps, 0
// is not a meaningful value here — an unbounded transcript is the bug
// compaction exists to fix — so a plain default is enough.
const DefaultContextTokens = engine.DefaultContextTokens

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

	// LogLevel is "debug", "info", "warn" or "error".
	LogLevel string `yaml:"log_level"`
	// LogBodies allows prompts, model replies and command output into
	// the log. Off by default: they carry secrets and bulk, and the
	// shape of a reply answers most questions.
	LogBodies bool `yaml:"log_bodies"`
	// LogDir holds one JSONL file per session; empty means the default
	// under ~/.local/state/detent/logs.
	LogDir string `yaml:"log_dir"`

	// Views is "saved" or "generate". See the constants.
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

	// MCP is the servers to connect at startup, keyed by the name
	// their tools are namespaced under. Unique by construction.
	MCP map[string]MCPServer `yaml:"mcp"`
}

// MCPServer is one server detent launches and speaks to over stdio.
type MCPServer struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
	// Env is what the server gets, on top of the basics a process
	// needs. Named deliberately: detent's own keys stay with detent.
	Env map[string]string `yaml:"env"`
	// Disabled keeps a server configured but unconnected, which beats
	// commenting a block out and losing it.
	Disabled bool `yaml:"disabled"`
}

// DefaultTheme mirrors theme.DefaultName, duplicated to avoid the same
// dependency.
const DefaultTheme = "dark"

// How far the output pane may go to draw a command's result. There is
// no "off": the pane draws from a spec either way, and the built-in
// rendering for a judged kind is one. The only question worth a
// setting is whether a model may write a new spec.
const (
	// ViewsSaved draws only from specs that already exist: the
	// built-in rendering for the judged kind, the ones detent ships
	// for known commands, and any saved on disk. Never calls a model.
	ViewsSaved = "saved"
	// ViewsGenerate does what ViewsSaved does first, and when nothing
	// covers an output, asks a model for a spec and saves it. The next
	// run of that command shape is served from disk for nothing.
	ViewsGenerate = "generate"
)

// DefaultViews draws from what already exists but spends no tokens.
// Generation is opt-in because it bills the proposer.
const DefaultViews = ViewsSaved

// DefaultLogLevel records what happened without recording everything.
const DefaultLogLevel = "info"

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		BaseURL:       DefaultBaseURL,
		Model:         DefaultModel,
		ContextTokens: DefaultContextTokens,
		JevModel:      classify.DefaultModel,
		Theme:         DefaultTheme,
		Views:         DefaultViews,
		LogLevel:      DefaultLogLevel,

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
