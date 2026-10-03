// cmd/detent runs the full-screen TUI, or one request headlessly with
// -prompt. It wires the engine, the bus, and whichever front-end.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/config"
	"github.com/vitzeno/detent/internal/engine"
	"github.com/vitzeno/detent/internal/forget"
	mcppkg "github.com/vitzeno/detent/internal/mcp"
	"github.com/vitzeno/detent/internal/model"
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
	err := run()
	if err == nil {
		return
	}
	code := 1
	var ended endedError
	if errors.As(err, &ended) {
		code = ended.code()
	}
	if msg := err.Error(); msg != "" {
		fmt.Fprintln(os.Stderr, "detent:", msg)
	}
	os.Exit(code)
}

// run reads top to bottom in the order a session opens. Each phase fills
// in sd as it goes, so an early return closes only what was reached.
func run() error {
	o := parseFlags()
	switch {
	case o.version:
		fmt.Println("detent", version.String())
		return nil
	case o.sessions:
		return listSessions()
	}
	if err := o.check(); err != nil {
		return err
	}

	trusted, cfg, err := configure(o)
	if err != nil {
		return err
	}
	// Before the ping and the log, which a command that only answers has no use for.
	switch {
	case o.prune:
		return pruneSandbox(cfg.SandboxSocket)
	case o.listMCP:
		return listServers(trusted.Trusted)
	}
	// Validated even for -prompt, so a typo fails fast either way.
	if err := applyLook(cfg); err != nil {
		return err
	}
	// Waited for once the sandbox is up, so the two overlap.
	pinged := ping(cfg)

	// A resumed session keeps its id, so its records continue the same
	// log rather than starting a second one beside it.
	id, restore, err := openSession(o.resume)
	if err != nil {
		return err
	}
	s := &session{o: o, cfg: cfg, trusted: trusted, id: id, restore: restore}
	s.warnTrust()
	// A session that cannot log is still a session: Setup says so and
	// carries on discarding.
	closeLog, logErr := logging.Setup(id.String(),
		logging.WithDir(cfg.LogDir),
		logging.WithLevel(cfg.LogLevel),
		logging.WithBodies(cfg.LogsBodies()))
	s.warn(logErr)
	defer func() { _ = closeLog() }()
	// Deferred after the log, so the log outlives everything it closes.
	s.sd.session = id
	defer func() { s.sd.close() }()

	if err := s.openSandbox(); err != nil {
		return err
	}
	if err := <-pinged; err != nil {
		return fmt.Errorf("%w\n\nis the model endpoint up? Wanted %s with model %s. Check the key, or point -url/-model (or a config file) somewhere else. For a local LM Studio, load the model and Start Server",
			err, cfg.BaseURL, cfg.Model)
	}
	if err := s.buildEngine(); err != nil {
		return err
	}
	ctx := s.wire()
	if o.prompt != "" {
		return s.runHeadless(ctx)
	}
	return s.runTUI(ctx)
}

// applyLook checks the config and applies its theme before anything draws.
func applyLook(cfg config.Config) error {
	th, ok := theme.Themes[cfg.Theme]
	if !ok {
		return fmt.Errorf("unknown theme %q, choose one of: %s", cfg.Theme, strings.Join(theme.Names(), ", "))
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	theme.Apply(th)
	ui.RefreshStyles()
	return nil
}

// endedError is a headless request that ended other than done, so a
// script driving -prompt can tell finished from gave up.
type endedError event.EndReason

func (e endedError) Error() string {
	if event.EndReason(e) == event.EndError {
		return "the request failed"
	}
	// The reason is already printed, so nothing more to say.
	return ""
}

func (e endedError) code() int {
	switch event.EndReason(e) {
	case event.EndAborted:
		return 130
	case event.EndBound:
		return 3
	case event.EndStopped:
		return 4
	default:
		return 1
	}
}

// composer wires the view composer. Only views: generate hands it the
// judge, and Validate refuses that mode without a jev key.
func composer(mode string, judge *classify.JevJudge) *viewgen.Generator {
	g := &viewgen.Generator{
		Registry: ui.Registry(),
		Store:    &viewgen.Store{Dir: viewgen.DefaultDir()},
		// Without a key nothing publishes CallJudged.
		Unjudged: judge == nil,
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
	defer func() { _ = events.Close() }()

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
	if c.SandboxMode != config.SandboxAuto {
		return ""
	}
	return c.SandboxSocket
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
