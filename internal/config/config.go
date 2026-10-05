// Package config loads the proposer, Jev judge and loop settings from a
// YAML file, under flags and environment and over built-in defaults. A
// missing file is not an error, but an unreadable -config path is.
package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/engine"
	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/sandbox"
)

// The model defaults are aliased from model rather than redeclared, so
// the two cannot drift.
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

// DefaultTheme mirrors theme.DefaultName, since config cannot import ui.
const DefaultTheme = "dark"

// The sandbox modes: a container when one can be had, or this machine.
const (
	SandboxAuto = "auto"
	SandboxHost = "host"
)

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

// LogLevels are the levels logging knows, quietest last.
var LogLevels = []string{"debug", "info", "warn", "error"}

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
	// FinishCheck asks the model to check its work before a request that changed
	// something ends. A pointer so unset means on rather than false.
	FinishCheck *bool `yaml:"finish_check"`
	// MCPTrustHints takes a server's word that a tool only reads, so it runs
	// unasked. A pointer so unset means on.
	MCPTrustHints *bool `yaml:"mcp_trust_hints"`

	JevAPIKey     string  `yaml:"jev_api_key"`
	JevModel      string  `yaml:"jev_model"`
	JevEndpoint   string  `yaml:"jev_endpoint"`
	RiskThreshold float64 `yaml:"risk_threshold"`

	// Theme is a name from ui/theme.Themes, checked by main.go so config need not import ui.
	Theme string `yaml:"theme"`

	// LogLevel is "debug", "info", "warn" or "error".
	LogLevel string `yaml:"log_level"`
	// LogBodies lets prompts, replies and output into the log, off by default for
	// secrets and bulk. A pointer, so env can turn off what the file turned on.
	LogBodies *bool `yaml:"log_bodies"`
	// LogDir holds one JSONL file per session, empty for logging.DefaultDir.
	LogDir string `yaml:"log_dir"`

	// Views is ViewsSaved or ViewsGenerate.
	Views string `yaml:"views"`

	// HostShell is what host commands run in, one of host.Dialects, or empty to pick per OS.
	HostShell string `yaml:"host_shell"`

	// SandboxMode is SandboxAuto or SandboxHost.
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

		// The sandbox is experimental, so commands run here unless asked for it.
		SandboxMode:      SandboxHost,
		SandboxImage:     sandbox.DefaultImage,
		SandboxNetwork:   sandbox.NetworkHost,
		SandboxWorkspace: DefaultSandboxWorkspace,
	}
}

// Load reads path, else ./.detent.y(a)ml from local (the bytes trust
// approved), else ~/.config/detent/config.y(a)ml, else returns Default.
func Load(path string, local map[string][]byte) (Config, error) {
	if path != "" {
		return read(path)
	}
	for _, name := range []string{".detent.yaml", ".detent.yml"} {
		if raw, ok := local[name]; ok {
			return decode(name, raw)
		}
	}
	for _, c := range userPaths() {
		_, err := os.Stat(c)
		switch {
		case err == nil:
			return read(c)
		case !errors.Is(err, fs.ErrNotExist):
			return Config{}, fmt.Errorf("config: %w", err)
		}
	}
	return Default(), nil
}

// example is the commented config detent ships, every value at its built-in.
//
//go:embed detent.example.yaml
var example []byte

// Example is the shipped config, which changes nothing until a value is set.
func Example() []byte { return slices.Clone(example) }

// UserPath is where the human's own config lives, ~/.config/detent/config.yaml.
func UserPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: no home directory: %w", err)
	}
	return filepath.Join(home, ".config", "detent", "config.yaml"), nil
}

// UserExists says whether the human has a config of their own, either spelling.
func UserExists() bool {
	for _, p := range userPaths() {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// WriteExample puts Example at path, owner-only since it will hold a key, and
// never over a file already there, whichever spelling it uses.
func WriteExample(path string) error {
	for _, p := range []string{path, strings.TrimSuffix(path, ".yaml") + ".yml"} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s already exists, so it was left as it is", p)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if _, err := f.Write(example); err != nil {
		return errors.Join(fmt.Errorf("config: %w", err), f.Close())
	}
	return f.Close()
}

// userPaths are the human's config files, in the order Load tries them.
func userPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, ".config", "detent")
	return []string{filepath.Join(dir, "config.yaml"), filepath.Join(dir, "config.yml")}
}

// Validate reports every value no part of detent can act on. Theme is
// checked by main, which can import ui.
func (c Config) Validate() error { return c.validate(runtime.GOOS) }

// CommandTimeoutDuration is CommandTimeout as a duration, the built-in when it is empty.
func (c Config) CommandTimeoutDuration() (time.Duration, error) {
	if c.CommandTimeout == "" {
		return engine.DefaultCommandTimeout, nil
	}
	d, err := time.ParseDuration(c.CommandTimeout)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("command_timeout %q: want a duration such as 30m", c.CommandTimeout)
	}
	return d, nil
}

// FinishChecks is FinishCheck with its default, on.
func (c Config) FinishChecks() bool { return c.FinishCheck == nil || *c.FinishCheck }

// TrustsMCPHints is MCPTrustHints with its default, on.
func (c Config) TrustsMCPHints() bool { return c.MCPTrustHints == nil || *c.MCPTrustHints }

// LogsBodies is LogBodies with its default, off.
func (c Config) LogsBodies() bool { return c.LogBodies != nil && *c.LogBodies }

// LogValue masks the two keys, so logging a Config cannot leak them.
func (c Config) LogValue() slog.Value {
	masked := c
	masked.APIKey, masked.JevAPIKey = mask(c.APIKey), mask(c.JevAPIKey)
	masked.Headers = nil
	return slog.AnyValue(plain(masked))
}

func (c Config) validate(goos string) error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	switch {
	case c.SandboxMode != SandboxAuto && c.SandboxMode != SandboxHost:
		bad("unknown sandbox mode %q, choose one of: %s, %s", c.SandboxMode, SandboxAuto, SandboxHost)
	case c.SandboxMode == SandboxAuto && goos == "windows":
		bad("sandbox_mode auto needs containerd on a unix socket, which Windows has not got: use sandbox_mode host")
	}
	if c.HostShell != "" && !slices.Contains(host.Dialects, c.HostShell) {
		bad("unknown host_shell %q, choose one of: %s, or leave it empty to pick per OS", c.HostShell, strings.Join(host.Dialects, ", "))
	}
	if c.SandboxNetwork != sandbox.NetworkHost && c.SandboxNetwork != sandbox.NetworkNone {
		bad("unknown sandbox_network %q, choose one of: %s, %s", c.SandboxNetwork, sandbox.NetworkHost, sandbox.NetworkNone)
	}
	switch c.Views {
	case ViewsSaved:
	case ViewsGenerate:
		if c.JevAPIKey == "" {
			bad("views: generate composes a view by asking the judge, so it needs jev_api_key (or TYPESAFE_API_KEY). Set one, or use views: saved")
		}
	default:
		bad("unknown views %q, choose one of: %s, %s", c.Views, ViewsSaved, ViewsGenerate)
	}
	if !slices.Contains(LogLevels, strings.ToLower(c.LogLevel)) {
		bad("unknown log_level %q, choose one of: %s", c.LogLevel, strings.Join(LogLevels, ", "))
	}
	if c.RiskThreshold < 0 || c.RiskThreshold > 1 {
		bad("risk_threshold %v: want a number from 0 to 1", c.RiskThreshold)
	}
	if c.Steps < 0 {
		bad("steps %d: want 0 for the built-in, or more", c.Steps)
	}
	if c.ContextTokens < 0 {
		bad("context_tokens %d: want 0 for the built-in, or more", c.ContextTokens)
	}
	if _, err := c.CommandTimeoutDuration(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func read(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return decode(path, raw)
}

func decode(path string, raw []byte) (Config, error) {
	cfg := Default()
	// Strict, so a misspelt key fails with its line instead of quietly doing nothing.
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// plain drops LogValue, or logging one would recurse.
type plain Config

func mask(key string) string {
	if key == "" {
		return ""
	}
	return "***"
}
