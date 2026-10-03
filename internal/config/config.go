// Package config loads the proposer, Jev judge and loop settings from a
// YAML file, under flags and environment and over built-in defaults. A
// missing file is not an error, but an unreadable -config path is.
package config

import (
	"bytes"
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

	JevAPIKey     string  `yaml:"jev_api_key"`
	JevModel      string  `yaml:"jev_model"`
	JevEndpoint   string  `yaml:"jev_endpoint"`
	RiskThreshold float64 `yaml:"risk_threshold"`

	// Theme is a name from ui/theme.Themes, checked by main.go so config need not import ui.
	Theme string `yaml:"theme"`

	// LogLevel is "debug", "info", "warn" or "error".
	LogLevel string `yaml:"log_level"`
	// LogBodies lets prompts, replies and command output into the log.
	// Off by default: they carry secrets and bulk. A pointer so env can turn off what the file turned on.
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

// FinishChecks is FinishCheck with its default, on.
func (c Config) FinishChecks() bool { return c.FinishCheck == nil || *c.FinishCheck }

// LogsBodies is LogBodies with its default, off.
func (c Config) LogsBodies() bool { return c.LogBodies != nil && *c.LogBodies }

// LogValue masks the two keys, so logging a Config cannot leak them.
func (c Config) LogValue() slog.Value {
	masked := c
	masked.APIKey, masked.JevAPIKey = mask(c.APIKey), mask(c.JevAPIKey)
	masked.Headers = nil
	return slog.AnyValue(plain(masked))
}

// plain drops LogValue, or logging one would recurse.
type plain Config

func mask(key string) string {
	if key == "" {
		return ""
	}
	return "***"
}

// Validate reports every value no part of detent can act on. Theme is
// checked by main, which can import ui.
func (c Config) Validate() error { return c.validate(runtime.GOOS) }

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
	if _, err := c.Timeout(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
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
	var candidates []string
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".config", "detent", "config.yaml"),
			filepath.Join(home, ".config", "detent", "config.yml"))
	}
	for _, c := range candidates {
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

// LogLevels are the levels logging knows, quietest last.
var LogLevels = []string{"debug", "info", "warn", "error"}

// The sandbox modes: a container when one can be had, or this machine.
const (
	SandboxAuto = "auto"
	SandboxHost = "host"
)

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
