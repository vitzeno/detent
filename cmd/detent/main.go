// cmd/detent runs the full-screen TUI, or one request headlessly with
// -prompt. It wires the engine, the bus, and whichever front-end.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/config"
	"github.com/vitzeno/detent/internal/engine"
	"github.com/vitzeno/detent/internal/forget"
	"github.com/vitzeno/detent/internal/headless"
	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/humanshell"
	"github.com/vitzeno/detent/internal/instructions"
	judgepkg "github.com/vitzeno/detent/internal/judge"
	mcppkg "github.com/vitzeno/detent/internal/mcp"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/routing"
	"github.com/vitzeno/detent/internal/sandbox"
	"github.com/vitzeno/detent/internal/store"
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

	baseURL := flag.String("url", "", "OpenAI-compatible base URL (default: config file, else "+config.DefaultBaseURL+")")
	modelName := flag.String("model", "", "model name (default: config file, else "+config.DefaultModel+")")
	apiKey := flag.String("key", "", "API key (default: config file, else env; empty for a local endpoint)")
	configPath := flag.String("config", "", "config file path (default: ./.detent.yaml, then ~/.config/detent/config.yaml)")
	prompt := flag.String("prompt", "", "run one request through the agent loop and exit")
	unattended := flag.Bool("unattended", false, "with -prompt, decline every flagged command instead of asking")
	approveAll := flag.Bool("approve-all", false, "with -prompt, run every flagged command without asking, only where nothing can be harmed, like a throwaway container")
	steps := flag.Int("steps", -1, "steps per request before it asks to continue (default: config file)")
	themeName := flag.String("theme", "", "color scheme: "+strings.Join(theme.Names(), ", ")+" (default: config file, else "+config.DefaultTheme+")")
	sandboxMode := flag.String("sandbox", "", "sandbox mode: auto, host (default: config file, else auto)")
	sandboxSocket := flag.String("sandbox-socket", "", "containerd socket path (default: config file, else OS-conventional)")
	resume := flag.String("resume", "", "continue a stored session by id or name, or \"last\"")
	sessions := flag.Bool("sessions", false, "list the sessions that can be resumed, and exit")
	prune := flag.Bool("prune", false, "remove what abandoned sessions left in containerd, and exit")
	listMCP := flag.Bool("mcp", false, "list the configured MCP servers and their tools, and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("detent", version.String())
		return nil
	}
	if *sessions {
		return listSessions()
	}
	if *unattended && *approveAll {
		return fmt.Errorf("-unattended declines every flagged command and -approve-all runs them, so pick one")
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

	// Validated even for -prompt, so a typo fails fast either way.
	th, ok := theme.Themes[resolved.Theme]
	if !ok {
		return fmt.Errorf("unknown theme %q, choose one of: %s", resolved.Theme, strings.Join(theme.Names(), ", "))
	}
	theme.Apply(th)
	ui.RefreshStyles()

	if resolved.Views == config.ViewsGenerate && resolved.JevAPIKey == "" {
		return fmt.Errorf("views: generate composes a view by asking the judge, so it needs jev_api_key (or TYPESAFE_API_KEY). Set one, or use views: saved")
	}
	if resolved.SandboxMode != "auto" && resolved.SandboxMode != "host" {
		return fmt.Errorf("unknown -sandbox %q, choose one of: auto, host", resolved.SandboxMode)
	}
	if resolved.SandboxNetwork != sandbox.NetworkHost && resolved.SandboxNetwork != sandbox.NetworkNone {
		return fmt.Errorf("unknown sandbox_network %q, choose one of: %s, %s",
			resolved.SandboxNetwork, sandbox.NetworkHost, sandbox.NetworkNone)
	}

	// Waited for below, so startup still fails fast. Nothing before the
	// wait needs the endpoint, so the ping overlaps it.
	pinged := make(chan error, 1)
	go func() {
		pinged <- model.Ping(context.Background(), resolved.BaseURL, resolved.APIKey)
	}()

	// A resumed session keeps its id, so its records continue the same
	// log rather than starting a second one beside it.
	sessionID, restore, err := openSession(*resume)
	if err != nil {
		return err
	}
	// A session that cannot log is still a session: Setup says so and
	// carries on discarding.
	closeLog, logErr := logging.Setup(sessionID.String(),
		logging.WithDir(resolved.LogDir),
		logging.WithLevel(resolved.LogLevel),
		logging.WithBodies(resolved.LogBodies))
	if logErr != nil {
		fmt.Fprintln(os.Stderr, logErr)
	}
	defer func() { _ = closeLog() }()

	// Filled in below as each thing opens. Deferred here so an early
	// return closes what was reached, and so the log outlives it.
	var sd shutdown
	sd.session = sessionID
	defer func() { sd.close() }()

	if *prune {
		return pruneSandbox(resolved.SandboxSocket)
	}
	if *listMCP {
		return listServers()
	}

	// Found before the container, which has to mount the ones outside the working directory.
	home, _ := os.UserHomeDir()
	found := findSkills(model.LocalEnvironment().Dir, home, resolved.SandboxWorkspace, resolved.SandboxMode == "auto")

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
			return fmt.Errorf("no default containerd socket for this OS: set -sandbox-socket (or sandbox_socket in config), or run with -sandbox host")
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
			sandbox.WithReadOnly(found.mounts),
		)
		if err := container.Start(context.Background(), sessionID.String()); err != nil {
			return fmt.Errorf("sandbox: starting container: %w", err)
		}
		sd.container = container
		runners.Sandbox = container

		// The container is Linux whatever this machine is, starts in the
		// mount point, and checkpoints the whole request together.
		env = model.Environment{
			OS: "linux", Arch: runtime.GOARCH, Dir: resolved.SandboxWorkspace,
			Sandboxed: true,
			Network:   resolved.SandboxNetwork == sandbox.NetworkHost,
			Undoable:  true,
		}
	}

	if err := <-pinged; err != nil {
		return fmt.Errorf("%v\n\nis the model endpoint up? Wanted %s with model %s. Check the key, or point -url/-model (or a config file) somewhere else. For a local LM Studio, load the model and Start Server",
			err, resolved.BaseURL, resolved.Model)
	}

	timeout, err := resolved.Timeout()
	if err != nil {
		return err
	}
	env.Timeout = timeout

	// Read on the host, where the files are, whichever runner the commands use.
	files, err := instructions.Find(model.LocalEnvironment().Dir, instructions.Global())
	if err != nil {
		fmt.Fprintln(os.Stderr, "instructions:", err)
	}
	client := &model.Client{
		BaseURL: resolved.BaseURL, Model: resolved.Model, APIKey: resolved.APIKey,
		Headers: resolved.Headers, Env: env,
		Instructions: instructions.Prompt(files), InstructionFiles: instructions.Paths(files),
	}

	// Opened before the engine so it can say whether this session is
	// being written down. A session that cannot be is still a session.
	events, storeErr := store.Open(store.DefaultPath())
	if storeErr != nil {
		fmt.Fprintln(os.Stderr, storeErr)
	}
	sd.events = events

	opts := []engine.Option{
		engine.WithSessionID(sessionID),
		engine.WithDescription(resolved.Model, judgeName(resolved),
			resolved.SandboxNetwork != sandbox.NetworkNone, events != nil),
		engine.WithContextTokens(resolved.ContextTokens),
		engine.WithCommandTimeout(timeout),
		engine.WithFinishCheck(resolved.FinishChecks()),
		engine.WithInstructions(instructions.Paths(files)),
		engine.WithSkills(found.summaries),
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

	// Connected before the engine, which takes the registry by value.
	// Headless reads none: config expands secrets, servers start processes.
	tools := tool.Standard(found.skillTools()...)
	configured, err := loadMCPConfig(*prompt == "", mcppkg.Files())
	if err != nil {
		return err
	}
	// Held now, filled later, so nothing waits on a server to draw.
	servers := mcppkg.NewInvokers()
	sd.servers = servers
	opts = append(opts, engine.WithInvoker(servers))

	bus := event.New()
	sd.bus = bus
	// Seeded before anything publishes, or a new record lands on an
	// ordinal already on disk. The replay never goes on the bus.
	bus.Resume(engine.Resumable(restore))
	eng := engine.New(bus, client, tools, runners, opts...)
	eng.Restore(restore)
	ctx, stop := context.WithCancel(context.Background())
	sd.stop = stop

	// Wired before Run so nothing published at startup is missed, and
	// unwatched by shutdown after it drains, so the last record lands.
	sd.unwatch = append(sd.unwatch, logging.Watch(bus))
	if events != nil {
		sd.unwatch = append(sd.unwatch, store.Watch(bus, events, sessionID))
	}
	if judge != nil {
		judgepkg.Watch(bus, judge)
		views(resolved.Views, judge).Watch(bus)
	}
	sd.unwatch = append(sd.unwatch, forget.Watch(bus, sessionStore(events), sessionID,
		forget.WithContainers(containerRemover(sandboxSocketFor(resolved)))))

	// After the watchers, so what this records is recorded too.
	announceResume(bus, sessionID, restore, env)

	if *prompt != "" {
		// Bubble Tea traps these for the TUI. Without it, a killed
		// -prompt would leave its container behind.
		ctx, untrap := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer untrap()

		for _, w := range found.warnings {
			fmt.Fprintln(os.Stderr, w)
		}
		sd.engine = runEngine(ctx, eng)
		approve := headless.Approver(nil)
		switch {
		case *unattended:
			approve = headless.AutoDecline
		case *approveAll:
			approve = headless.AutoApprove
		}
		reason := headless.Run(ctx, bus, *prompt, approve)
		fmt.Printf("\nrequest ended: %s\n", reason)
		if reason == event.EndError {
			return fmt.Errorf("the request failed")
		}
		return nil
	}

	info := ui.SessionInfo{
		Image: resolved.SandboxImage, Mount: resolved.SandboxWorkspace,
		Runtime: resolved.SandboxRuntime, Network: resolved.SandboxNetwork,
	}
	// Built before the engine runs: SessionStarted is published once,
	// and a front-end that subscribes afterwards loses it.
	model := ui.New(ctx, bus, info).Restore(restore)
	for _, w := range found.warnings {
		bus.Publish(event.Notice{Level: "warn", Text: w})
	}
	// The same runner the model's commands go to: a shell that cannot
	// see what the agent just did is not worth having.
	shellRunner, shellWhere := runners.Select(event.UnknownRisk())
	sd.shell = humanshell.Watch(bus, shellRunner, shellWhere)
	signins := mcppkg.NewSignIns(bus, servers, mcppkg.Tokens{Dir: mcppkg.TokensDir()}, openBrowser)
	sd.unwatch = append(sd.unwatch, mcppkg.Watch(bus, servers,
		mcppkg.Redialer(ctx, tools, servers, configured, signins), signins))
	sd.connect = connectServers(ctx, bus, tools, servers, configured, signins)
	sd.engine = runEngine(ctx, eng)

	// Altscreen is declared by ui.Model.View: under Bubble Tea v2,
	// terminal state is a property of what is rendered.
	p := tea.NewProgram(model)
	_, err = p.Run()
	return err
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

// loadDotenv fills gaps from .env. Real environment variables always win.
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

// views wires the composer. Only views: generate hands it the judge,
// and run() refuses that mode without a jev key.
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

// openSession picks a stored session to continue, or a new one, and
// returns the records the engine and UI rebuild themselves from.
func openSession(resume string) (uuid.UUID, []event.Record, error) {
	if resume == "" {
		return uuid.Must(uuid.NewV7()), nil, nil
	}
	events, err := store.Open(store.DefaultPath())
	if err != nil {
		return uuid.Nil, nil, err
	}
	defer events.Close()

	id, err := resolveSession(events, resume)
	if err != nil {
		return uuid.Nil, nil, err
	}
	records, err := events.Replay(id)
	if err != nil {
		return uuid.Nil, nil, err
	}
	if len(records) == 0 {
		return uuid.Nil, nil, fmt.Errorf("session %s has nothing recorded", id)
	}
	return id, records, nil
}

// resolveSession reads an id, then "last", then a name. store.Rename
// refuses a name shaped like the first two, since it would never be read.
func resolveSession(events *store.Store, want string) (uuid.UUID, error) {
	if id, err := uuid.Parse(want); err == nil {
		return id, nil
	}
	all, err := events.Sessions()
	if err != nil {
		return uuid.Nil, err
	}
	if len(all) == 0 {
		return uuid.Nil, fmt.Errorf("no sessions recorded yet")
	}
	if strings.EqualFold(want, store.ReservedName) {
		return all[0].ID, nil
	}
	for _, s := range all {
		if strings.EqualFold(s.Name, want) {
			return s.ID, nil
		}
	}
	return uuid.Nil, fmt.Errorf("no session named %q, try -sessions or -resume %s", want, store.ReservedName)
}

// judgeName is what the session says classified it, empty when no key
// was set and nothing did.
func judgeName(c config.Config) string {
	if c.JevAPIKey == "" {
		return ""
	}
	return c.JevModel
}

// sessionStore avoids handing forget a non-nil interface holding a
// nil pointer, which is not nil and would panic on the first call.
func sessionStore(s *store.Store) forget.Sessions {
	if s == nil {
		return nil
	}
	return s
}

// containerRemover is how a deleted session's container goes, or nil
// when this run has no sandbox and so nothing to remove.
func containerRemover(socket string) func(context.Context, string) error {
	if socket == "" {
		return nil
	}
	return func(ctx context.Context, id string) error {
		err := sandbox.Forget(ctx, socket, sandbox.DefaultNamespace, id)
		if errors.Is(err, sandbox.ErrSessionLive) {
			return fmt.Errorf("%w: %w", forget.ErrLive, err)
		}
		return err
	}
}

// sandboxSocketFor is the socket a deleted session's container would
// be on, or "" when this run never had one to speak of.
func sandboxSocketFor(c config.Config) string {
	if c.SandboxMode != "auto" {
		return ""
	}
	if c.SandboxSocket != "" {
		return c.SandboxSocket
	}
	return defaultSandboxSocket()
}

// loadMCPConfig keeps MCP configuration out of headless runs entirely.
// It is enabled for the TUI, where users can inspect and invoke servers.
func loadMCPConfig(enabled bool, files []string) (map[string]mcppkg.Config, error) {
	if !enabled {
		return map[string]mcppkg.Config{}, nil
	}
	return mcppkg.Load(files...)
}

// connectServers dials MCP without holding up the first frame, publishing
// each server as it settles. The channel closes when done, for shutdown.
func connectServers(ctx context.Context, bus *event.Bus, tools *tool.Registry,
	servers *mcppkg.Invokers, configured map[string]mcppkg.Config, signins *mcppkg.SignIns) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		connectAll(ctx, bus, tools, servers, configured, signins)
	}()
	return done
}

func connectAll(ctx context.Context, bus *event.Bus, tools *tool.Registry,
	servers *mcppkg.Invokers, configured map[string]mcppkg.Config, signins *mcppkg.SignIns) {
	if len(configured) == 0 {
		return
	}
	errs := mcppkg.ConnectAll(ctx, tools, servers, configured, func(s event.ServerSummary) {
		if s.Err != "" {
			bus.Publish(event.Notice{Level: "error", Text: s.Err})
		}
		bus.Publish(event.ServersListed{Servers: servers.Status()})
	}, mcppkg.WithSignIns(signins))
	if n := len(servers.Servers()); n > 0 && len(errs) == 0 {
		bus.Publish(event.Notice{Level: "info", Text: fmt.Sprintf("%d mcp server(s) ready", n)})
	}
}

// runEngine starts the loop and says when it has stopped, which is what
// shutdown waits on before closing the bus out from under it.
func runEngine(ctx context.Context, eng *engine.Engine) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		eng.Run(ctx)
	}()
	return done
}

// The engine finds PromptSizer by type assertion, so a renamed method
// would empty the context page's prompt rows rather than fail the build.
var _ engine.PromptSizer = (*model.Client)(nil)
