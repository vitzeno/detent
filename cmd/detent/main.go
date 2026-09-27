// cmd/detent runs the full-screen TUI, or one request headlessly with
// -prompt. It wires the engine, the bus, and whichever front-end.
package main

import (
	"bufio"
	"context"
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
		sd.container = container
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
		return fmt.Errorf("%v\n\nis the model endpoint up? Wanted %s with model %s — check the key, or point -url/-model (or a config file) somewhere else. For a local LM Studio, load the model and Start Server",
			err, resolved.BaseURL, resolved.Model)
	}

	client := &model.Client{
		BaseURL: resolved.BaseURL, Model: resolved.Model, APIKey: resolved.APIKey,
		Headers: resolved.Headers, Env: env,
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

	// Connected before the engine, which takes the registry by value
	// and would never see a tool added afterwards.
	tools := tool.Standard()
	configured, err := mcppkg.Load(mcppkg.Files()...)
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
	sd.unwatch = append(sd.unwatch, mcppkg.Watch(bus, servers))
	sd.unwatch = append(sd.unwatch, forget.Watch(bus, sessionStore(events), sessionID,
		forget.WithContainers(containerRemover(sandboxSocketFor(resolved)))))

	// A resumed transcript describes a machine that no longer exists.
	// After the watchers, so the append it causes is recorded too.
	if len(restore) > 0 {
		bus.Publish(event.NoteContext{Text: resumeNote(restore, env)})
	}

	if *prompt != "" {
		// Bubble Tea catches these for the TUI; with no TUI, nothing
		// does so a killed -prompt leaves its container behind.
		ctx, untrap := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer untrap()

		// Waited for here: one prompt goes out immediately, and a tool
		// that lands after it may as well not exist.
		for _, e := range mcppkg.ConnectAll(ctx, tools, servers, configured, nil) {
			fmt.Fprintln(os.Stderr, "warning:", e)
		}
		sd.engine = runEngine(ctx, eng)
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

	info := ui.SessionInfo{
		Image: resolved.SandboxImage, Mount: resolved.SandboxWorkspace,
		Runtime: resolved.SandboxRuntime, Network: resolved.SandboxNetwork,
	}
	// Built before the engine runs: SessionStarted is published once,
	// and a front-end that subscribes afterwards loses it.
	model := ui.New(ctx, bus, info).Restore(restore)
	// The same runner the model's commands go to: a shell that cannot
	// see what the agent just did is not worth having.
	shellRunner, shellWhere := runners.Select(event.UnknownRisk())
	sd.shell = humanshell.Watch(bus, shellRunner, shellWhere)
	sd.connect = connectServers(ctx, bus, tools, servers, configured)
	sd.engine = runEngine(ctx, eng)

	// Altscreen is declared by ui.Model.View, not set here — under
	// Bubble Tea v2 terminal state is a property of what's rendered.
	p := tea.NewProgram(model)
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

// views wires the composer. The judge is what writes a spec, so
// views: generate without a jev key has nothing to compose with and
// run() rejects that rather than quietly drawing from saved only.
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

// openSession picks the session to run: a stored one to continue, or
// a new one. The records come back for the engine and the UI to
// rebuild themselves from.
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

// resolveSession reads an id, then "last", then a name, because
// nobody remembers a uuid. That order is why store.Rename refuses a
// name shaped like either of the first two: it would never be read.
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
	return uuid.Nil, fmt.Errorf("no session named %q — try -sessions, or -resume %s", want, store.ReservedName)
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
		return sandbox.Forget(ctx, socket, sandbox.DefaultNamespace, id)
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

// connectServers wires MCP without holding up the first frame, and
// publishes each server as it settles so an open /mcp fills in. The
// channel closes when it is done, which is what shutdown waits on.
func connectServers(ctx context.Context, bus *event.Bus, tools *tool.Registry,
	servers *mcppkg.Invokers, configured map[string]mcppkg.Config) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		connectAll(ctx, bus, tools, servers, configured)
	}()
	return done
}

func connectAll(ctx context.Context, bus *event.Bus, tools *tool.Registry,
	servers *mcppkg.Invokers, configured map[string]mcppkg.Config) {
	if len(configured) == 0 {
		return
	}
	errs := mcppkg.ConnectAll(ctx, tools, servers, configured, func(s event.ServerSummary) {
		if s.Err != "" {
			bus.Publish(event.Notice{Level: "error", Text: s.Err})
		}
		bus.Publish(event.ServersListed{Servers: servers.Status()})
	})
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
