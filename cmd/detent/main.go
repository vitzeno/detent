// cmd/detent runs the full-screen TUI, or one request headlessly with
// -prompt. It wires the engine, the bus, and whichever front-end.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/config"
	"github.com/vitzeno/detent/internal/engine"
	"github.com/vitzeno/detent/internal/headless"
	"github.com/vitzeno/detent/internal/host"
	judgepkg "github.com/vitzeno/detent/internal/judge"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/routing"
	"github.com/vitzeno/detent/internal/sandbox"
	"github.com/vitzeno/detent/internal/tool"
	"github.com/vitzeno/detent/internal/viewgen"
	"github.com/vitzeno/detent/logging"
	"github.com/vitzeno/detent/ui"
	"github.com/vitzeno/detent/ui/theme"
	"github.com/vitzeno/detent/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "detent:", err)
		os.Exit(1)
	}
}

func run() error {
	loadDotenv(".env")

	baseURL := flag.String("url", "", "OpenAI-compatible base URL (default: config file, else LM Studio local)")
	modelName := flag.String("model", "", "model name (default: config file, else "+config.DefaultModel+")")
	apiKey := flag.String("key", "", "API key (default: config file, else env; empty for local LM Studio)")
	configPath := flag.String("config", "", "config file path (default: ./.detent.yaml, then ~/.config/detent/config.yaml)")
	prompt := flag.String("prompt", "", "run one request through the agent loop and exit")
	unattended := flag.Bool("unattended", false, "with -prompt, decline every flagged command instead of asking")
	steps := flag.Int("steps", -1, "steps per request before it asks to continue (default: config file)")
	themeName := flag.String("theme", "", "color scheme: "+strings.Join(theme.Names(), ", ")+" (default: config file, else "+config.DefaultTheme+")")
	sandboxMode := flag.String("sandbox", "", "sandbox mode: auto, host (default: config file, else auto)")
	sandboxSocket := flag.String("sandbox-socket", "", "containerd socket path (default: config file, else OS-conventional)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("detent", version.String())
		return nil
	}

	fileCfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	flagCfg := config.Config{
		BaseURL: *baseURL, Model: *modelName, APIKey: *apiKey, Theme: *themeName,
		SandboxMode: *sandboxMode, SandboxSocket: *sandboxSocket,
	}
	resolved := config.Resolve(fileCfg, flagCfg, *steps)

	// Validated even for -goal, so a typo fails fast either way.
	th, ok := theme.Themes[resolved.Theme]
	if !ok {
		return fmt.Errorf("unknown theme %q — choose one of: %s", resolved.Theme, strings.Join(theme.Names(), ", "))
	}
	theme.Apply(th)
	ui.RefreshStyles()

	if resolved.Views == config.ViewsGenerate && resolved.JevAPIKey == "" {
		return fmt.Errorf("views: generate composes a view by asking the judge, so it needs jev_api_key (or TYPESAFE_API_KEY) — set one, or use views: saved")
	}
	if resolved.SandboxMode != "auto" && resolved.SandboxMode != "host" {
		return fmt.Errorf("unknown -sandbox %q — choose one of: auto, host", resolved.SandboxMode)
	}
	if resolved.SandboxNetwork != sandbox.NetworkHost && resolved.SandboxNetwork != sandbox.NetworkNone {
		return fmt.Errorf("unknown sandbox_network %q — choose one of: %s, %s",
			resolved.SandboxNetwork, sandbox.NetworkHost, sandbox.NetworkNone)
	}

	// Waited for below, so it still fails fast with a clear message.
	// Everything between here and the wait needs no endpoint, and this
	// was ~270ms of the time before anything drew.
	pinged := make(chan error, 1)
	go func() {
		pinged <- model.Ping(context.Background(), resolved.BaseURL, resolved.APIKey)
	}()

	sessionID := uuid.Must(uuid.NewV7())
	// A session that cannot log is still a session: Setup says so and
	// carries on discarding.
	closeLog, err := logging.Setup(logging.Options{
		Dir: resolved.LogDir, Session: sessionID.String(),
		Level: resolved.LogLevel, Bodies: resolved.LogBodies,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	defer func() { _ = closeLog() }()
	runners := routing.Selector{Host: host.NewShell(), HostOnly: resolved.SandboxMode == "host"}
	var container *sandbox.Container
	// The model is told where commands actually run, so it writes for
	// that OS and knows what survives.
	env := model.LocalEnvironment()
	if resolved.SandboxMode == "auto" {
		socket := resolved.SandboxSocket
		if socket == "" {
			socket = defaultSandboxSocket()
		}
		if socket == "" {
			return fmt.Errorf("no default containerd socket for this OS — set -sandbox-socket (or sandbox_socket in config), or run with -sandbox host")
		}

		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := sandbox.Preflight(pctx, socket)
		cancel()
		if err != nil {
			return fmt.Errorf("%w\n\nis containerd reachable at %s? check the colima/containerd socket is up, or run with -sandbox host", err, socket)
		}

		container = sandbox.NewContainer(
			sandbox.WithSocket(socket),
			sandbox.WithImage(resolved.SandboxImage),
			sandbox.WithMountPoint(resolved.SandboxWorkspace),
			sandbox.WithRuntime(resolved.SandboxRuntime),
			sandbox.WithNetwork(resolved.SandboxNetwork),
		)
		if err := container.Start(context.Background(), sessionID.String()); err != nil {
			return fmt.Errorf("sandbox: starting container: %w", err)
		}
		defer container.Close(context.Background())
		runners.Sandbox = container

		// The container is Linux whatever this machine is, starts in
		// the mount point rather than here, and the whole request is
		// checkpointed together.
		env = model.Environment{
			OS: "linux", Arch: runtime.GOARCH, Dir: resolved.SandboxWorkspace,
			Sandboxed: true,
			Network:   resolved.SandboxNetwork == sandbox.NetworkHost,
			Undoable:  true,
		}
	}

	if err := <-pinged; err != nil {
		return fmt.Errorf("%v\n\nis the model endpoint up? Wanted %s with model %s — for LM Studio, load the model and Start Server; otherwise point -url/-model (or a config file) at your provider",
			err, resolved.BaseURL, resolved.Model)
	}

	client := &model.Client{
		BaseURL: resolved.BaseURL, Model: resolved.Model, APIKey: resolved.APIKey,
		Headers: resolved.Headers, Env: env,
	}

	opts := []engine.Option{
		engine.WithSessionID(sessionID),
		engine.WithContextTokens(resolved.ContextTokens),
		engine.WithMaxSteps(resolved.Steps),
		// The same endpoint compacts its own history when it outgrows
		// that budget.
		engine.WithSummarizer(client),
	}
	// No judge without a key: TYPESAFE_API_KEY env or jev_api_key file.
	var judge *classify.JevJudge
	if resolved.JevAPIKey != "" {
		judge = classify.NewJevJudge(resolved.JevAPIKey,
			classify.WithModel(resolved.JevModel),
			classify.WithEndpoint(resolved.JevEndpoint))
		opts = append(opts, engine.WithJudge(
			classify.RiskJudge{Asker: judge, Threshold: resolved.RiskThreshold},
			resolved.RiskThreshold))
	}

	bus := event.New()
	eng := engine.New(bus, client, tool.Standard(), runners, opts...)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	// Subscribers, wired before Run so nothing published at startup is
	// missed. Each is independent: dropping one costs its own feature
	// and nothing else.
	// Registered first so it runs last: Drain empties the bus, then
	// this waits for the final record to reach disk.
	defer logging.Watch(bus)()
	if judge != nil {
		judgepkg.Watch(bus, judge)
		views(resolved.Views, judge).Watch(bus)
	}
	// Drained rather than closed, so the last records reach the log
	// instead of dying with the process.
	defer bus.Drain(2 * time.Second)
	go eng.Run(ctx)

	if *prompt != "" {
		approve := headless.Approver(nil)
		if *unattended {
			approve = headless.AutoDecline
		}
		reason := headless.Run(ctx, bus, *prompt, approve)
		fmt.Printf("\nrequest ended: %s\n", reason)
		if reason == event.EndError {
			return fmt.Errorf("the request failed")
		}
		return nil
	}

	judgeName := ""
	if judge != nil {
		judgeName = resolved.JevModel
	}
	// The effective mode, not the configured one: "auto" still reports
	// host when no sandbox ended up wired.
	_, runMode := runners.Select(event.UnknownRisk())
	info := ui.SessionInfo{
		Model: resolved.Model, Judge: judgeName, RunMode: runMode,
		Image: resolved.SandboxImage, Mount: resolved.SandboxWorkspace,
		Runtime: resolved.SandboxRuntime, Network: resolved.SandboxNetwork,
	}
	// Altscreen is declared by ui.Model.View, not set here — under
	// Bubble Tea v2 terminal state is a property of what's rendered.
	p := tea.NewProgram(ui.New(ctx, bus, info))
	_, err = p.Run()
	return err
}

// defaultSandboxSocket returns the OS-conventional containerd socket
// path, or "" when there's no safe default (Windows, or an
// unrecognized OS), in which case run() requires an explicit override.
func defaultSandboxSocket() string {
	switch runtime.GOOS {
	case "linux":
		return "/run/containerd/containerd.sock"
	case "darwin":
		// colima --runtime containerd, default profile name.
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, ".colima", "default", "containerd.sock")
	default:
		return ""
	}
}

// loadDotenv fills gaps from .env; real environment variables always win.
func loadDotenv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
}

// views wires the composer. Composition is the only way a spec gets
// written now, and the judge is what writes it, so views: generate
// without a jev key has nothing to compose with; run() rejects that
// combination rather than silently drawing from saved specs only.
func views(mode string, judge *classify.JevJudge) *viewgen.Generator {
	g := &viewgen.Generator{
		Registry: ui.Registry(),
		Store:    &viewgen.Store{Dir: viewgen.DefaultDir()},
	}
	if mode == config.ViewsGenerate && judge != nil {
		g.Judge = judge
	}
	return g
}
