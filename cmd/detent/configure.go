package main

import (
	"bufio"
	"bytes"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vitzeno/detent/internal/config"
	"github.com/vitzeno/detent/internal/trust"
	"github.com/vitzeno/detent/ui/theme"
)

// options is every flag, read once.
type options struct {
	baseURL, modelName, apiKey, configPath string
	prompt                                 string
	unattended, approveAll                 bool
	steps                                  int
	themeName                              string
	sandboxMode, sandboxSocket             string
	resume                                 string
	sessions, prune, listMCP, version      bool
	trust                                  bool
}

func parseFlags() options {
	var o options
	flag.StringVar(&o.baseURL, "url", "", "OpenAI-compatible base URL (default: env, else config file, else "+config.DefaultBaseURL+")")
	flag.StringVar(&o.modelName, "model", "", "model name (default: env, else config file, else "+config.DefaultModel+")")
	flag.StringVar(&o.apiKey, "key", "", "API key, better set as DETENT_API_KEY since a flag shows in ps (default: env, else config file; empty for a local endpoint)")
	flag.StringVar(&o.configPath, "config", "", "config file path (default: ./.detent.yaml, then ~/.config/detent/config.yaml)")
	flag.StringVar(&o.prompt, "prompt", "", "run one request through the agent loop and exit")
	flag.BoolVar(&o.unattended, "unattended", false, "with -prompt, decline every flagged command instead of asking")
	flag.BoolVar(&o.approveAll, "approve-all", false, "with -prompt, run every flagged command without asking, only where nothing can be harmed, like a throwaway container")
	flag.IntVar(&o.steps, "steps", -1, "steps per request before it asks to continue (default: config file)")
	flag.StringVar(&o.themeName, "theme", "", "color scheme: "+strings.Join(theme.Names(), ", ")+" (default: env, else config file, else "+config.DefaultTheme+")")
	flag.StringVar(&o.sandboxMode, "sandbox", "", "sandbox mode: host, or auto for the experimental containerd sandbox (default: env, else config file, else host)")
	flag.StringVar(&o.sandboxSocket, "sandbox-socket", "", "containerd socket path (default: env, else config file, else OS-conventional)")
	flag.StringVar(&o.resume, "resume", "", "continue a stored session by id or name, or \"last\"")
	flag.BoolVar(&o.sessions, "sessions", false, "list the sessions that can be resumed, and exit")
	flag.BoolVar(&o.prune, "prune", false, "remove what abandoned sessions left in containerd, and exit")
	flag.BoolVar(&o.listMCP, "mcp", false, "list the configured MCP servers and their tools, and exit")
	flag.BoolVar(&o.version, "version", false, "print the version and exit")
	flag.BoolVar(&o.trust, "trust", false, "read this directory's .detent.yaml, .env and .mcp.json for this run without asking or recording it")
	flag.Parse()
	return o
}

// check refuses flags that contradict each other or do nothing.
func (o options) check() error {
	if o.unattended && o.approveAll {
		return errors.New("-unattended declines every flagged command and -approve-all runs them, so pick one")
	}
	if (o.unattended || o.approveAll) && o.prompt == "" {
		return errors.New("-unattended and -approve-all only apply with -prompt: the TUI always asks")
	}
	return nil
}

// configure decides trust before anything the repo carries is read, since
// it can redirect the key or start programs, then layers the config.
func configure(o options) (trust.Decision, config.Config, error) {
	trusted, err := trust.Decide(trust.Options{Dir: ".", State: trust.DefaultDir(),
		Flag: o.trust, Ask: askTrust(o.prompt == ""), Out: os.Stderr})
	if err != nil {
		return trusted, config.Config{}, err
	}
	cfg, err := layer(o, trusted)
	return trusted, cfg, err
}

// askTrust asks on the terminal, or is nil when nobody is there to answer.
func askTrust(tui bool) func() bool {
	if !tui || !isTerminal(os.Stdin) || !isTerminal(os.Stderr) {
		return nil
	}
	return trust.Reader(os.Stdin)
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// layer reads the bytes trust hashed rather than the disk, so a file
// changed since it asked cannot slip in.
func layer(o options, trusted trust.Decision) (config.Config, error) {
	if err := loadDotenv(trusted.Files[".env"]); err != nil {
		return config.Config{}, err
	}
	file, err := config.Load(o.configPath, trusted.Files)
	if err != nil {
		return config.Config{}, err
	}
	flags := config.Config{
		BaseURL: o.baseURL, Model: o.modelName, APIKey: o.apiKey, Theme: o.themeName,
		SandboxMode: o.sandboxMode, SandboxSocket: o.sandboxSocket,
	}
	cfg := config.Resolve(file, flags, o.steps)
	cfg.SandboxSocket = cmp.Or(cfg.SandboxSocket, defaultSandboxSocket())
	return cfg, nil
}

// loadDotenv fills gaps from the working directory's .env, as approved.
// Real environment variables always win, even set to "".
func loadDotenv(raw []byte) error {
	const path = ".env"
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		key, value, ok := dotenvLine(scanner.Text())
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// dotenvLine reads one KEY=value line: an export prefix, one matched pair
// of quotes, and a trailing # comment on an unquoted value are allowed.
func dotenvLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	key, value, ok = strings.Cut(strings.TrimPrefix(line, "export "), "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return "", "", false
	}
	value = strings.TrimSpace(value)
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return key, value[1 : len(value)-1], true
	}
	if i := strings.Index(value, " #"); i >= 0 {
		value = strings.TrimSpace(value[:i])
	}
	return key, value, true
}

// defaultSandboxSocket returns the OS-conventional containerd socket,
// or "" when there is no safe default and run() needs an override.
func defaultSandboxSocket() string {
	switch runtime.GOOS {
	case "linux":
		return "/run/containerd/containerd.sock"
	case "darwin":
		// colima's default profile, whichever runtime it was started with.
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, ".colima", "default", "containerd.sock")
	default:
		return ""
	}
}
