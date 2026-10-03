package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/config"
	"github.com/vitzeno/detent/internal/engine"
	"github.com/vitzeno/detent/internal/forget"
	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/instructions"
	judgepkg "github.com/vitzeno/detent/internal/judge"
	mcppkg "github.com/vitzeno/detent/internal/mcp"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/routing"
	"github.com/vitzeno/detent/internal/sandbox"
	"github.com/vitzeno/detent/internal/store"
	"github.com/vitzeno/detent/internal/tool"
	"github.com/vitzeno/detent/internal/trust"
	"github.com/vitzeno/detent/logging"
)

// session is what one run opens, filled in phase by phase. sd closes it.
type session struct {
	o       options
	cfg     config.Config
	trusted trust.Decision
	id      uuid.UUID
	restore []event.Record
	sd      shutdown
	// Said once something can show them: printed headless, a Notice in
	// the TUI, whose screen would otherwise hide anything printed now.
	warnings []string

	// openSandbox
	local   model.Environment
	env     model.Environment
	found   foundSkills
	runners routing.Selector

	// buildEngine
	events     *store.Store
	judge      *classify.JevJudge
	tools      *tool.Registry
	configured map[string]mcppkg.Config
	servers    *mcppkg.Invokers
	bus        *event.Bus
	eng        *engine.Engine
}

func (s *session) warn(err error) {
	if err != nil {
		s.warnings = append(s.warnings, err.Error())
	}
}

// warnTrust repeats what Decide printed, which the TUI's alt screen hides.
func (s *session) warnTrust() {
	if !s.trusted.Trusted && len(s.trusted.Present) > 0 && s.o.prompt == "" {
		s.warnings = append(s.warnings, "ignoring "+strings.Join(s.trusted.Present, ", ")+": this directory is not trusted")
	}
}

// ping checks the endpoint in the background. Waited for after the
// sandbox, so startup still fails fast but the two overlap.
func ping(cfg config.Config) <-chan error {
	pinged := make(chan error, 1)
	go func() {
		pinged <- (&model.Client{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Headers: cfg.Headers}).Ping(context.Background())
	}()
	return pinged
}

// openSandbox decides where commands run and starts the container if
// they run in one. The model is told which, so it writes for that OS.
func (s *session) openSandbox() error {
	s.local = model.LocalEnvironment()
	s.env = s.local
	sandboxed := s.cfg.SandboxMode == config.SandboxAuto
	// Found before the container, which has to mount the ones outside the working directory.
	home, _ := os.UserHomeDir()
	s.found = findSkills(s.local.Dir, home, s.cfg.SandboxWorkspace, sandboxed)
	s.warnings = append(s.warnings, s.found.warnings...)
	s.runners = routing.Selector{Host: host.NewShell(), HostOnly: !sandboxed}
	if !sandboxed {
		return nil
	}

	socket := s.cfg.SandboxSocket
	if socket == "" {
		return errors.New("no default containerd socket for this OS: set -sandbox-socket (or sandbox_socket in config), or run with -sandbox host")
	}
	pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := sandbox.Preflight(pctx, socket)
	cancel()
	if err != nil {
		return fmt.Errorf("%w\n\nis containerd reachable at %s? check the colima/containerd socket is up, or run with -sandbox host", err, socket)
	}
	container := sandbox.NewContainer(
		sandbox.WithSocket(socket),
		sandbox.WithImage(s.cfg.SandboxImage),
		sandbox.WithMountPoint(s.cfg.SandboxWorkspace),
		sandbox.WithRuntime(s.cfg.SandboxRuntime),
		sandbox.WithNetwork(s.cfg.SandboxNetwork),
		sandbox.WithReadOnly(s.found.mounts),
	)
	if err := container.Start(context.Background(), s.id.String()); err != nil {
		return fmt.Errorf("sandbox: starting container: %w", err)
	}
	s.sd.container = container
	s.runners.Sandbox = container

	// The container is Linux whatever this machine is, starts in the
	// mount point, and checkpoints the whole request together.
	s.env = model.Environment{
		OS: "linux", Arch: runtime.GOARCH, Dir: s.cfg.SandboxWorkspace,
		Sandboxed: true,
		Network:   s.cfg.SandboxNetwork == sandbox.NetworkHost,
		Undoable:  true,
	}
	return nil
}

// buildEngine assembles the client, the store, the judge, the tools and
// the bus, then the engine over them, restored from any resumed records.
func (s *session) buildEngine() error {
	timeout, err := s.cfg.Timeout()
	if err != nil {
		return err
	}
	s.env.Timeout = timeout

	// Read on the host, where the files are, whichever runner the commands use.
	files, err := instructions.Find(s.local.Dir, instructions.Global())
	if err != nil {
		s.warnings = append(s.warnings, "instructions: "+err.Error())
	}
	client := &model.Client{
		BaseURL: s.cfg.BaseURL, Model: s.cfg.Model, APIKey: s.cfg.APIKey,
		Headers: s.cfg.Headers, Env: s.env,
		Instructions: instructions.Prompt(files), InstructionFiles: instructions.Paths(files),
	}

	// Opened before the engine so it can say whether this session is
	// being written down. A session that cannot be is still a session.
	events, storeErr := store.Open(store.DefaultPath())
	s.warn(storeErr)
	s.events, s.sd.events = events, events

	opts := []engine.Option{
		engine.WithSessionID(s.id),
		engine.WithDescription(s.cfg.Model, judgeName(s.cfg),
			s.cfg.SandboxNetwork != sandbox.NetworkNone, events != nil),
		engine.WithContextTokens(s.cfg.ContextTokens),
		engine.WithCommandTimeout(timeout),
		engine.WithFinishCheck(s.cfg.FinishChecks()),
		engine.WithInstructions(instructions.Paths(files)),
		engine.WithSkills(s.found.summaries),
		engine.WithMaxSteps(s.cfg.Steps),
		// The same endpoint compacts its own history when it outgrows
		// that budget.
		engine.WithSummarizer(client),
	}
	if wt, err := openWorktree(s.o.prompt == "", s.local.Dir); err != nil {
		s.warn(err)
	} else if wt != nil {
		opts = append(opts, engine.WithWorktree(wt))
	}
	// No judge without a key: TYPESAFE_API_KEY env or jev_api_key file.
	if s.cfg.JevAPIKey != "" {
		s.judge = classify.NewJevJudge(s.cfg.JevAPIKey,
			classify.WithModel(s.cfg.JevModel),
			classify.WithEndpoint(s.cfg.JevEndpoint))
		opts = append(opts, engine.WithJudge(
			classify.RiskJudge{Asker: s.judge, Threshold: s.cfg.RiskThreshold},
			s.cfg.RiskThreshold))
	}

	// Connected before the engine, which takes the registry by value.
	// Headless reads none: config expands secrets, servers start processes.
	s.tools = tool.Standard(s.found.skillTools()...)
	s.configured, err = loadMCPConfig(s.o.prompt == "", mcppkg.Files(s.trusted.Trusted))
	if err != nil {
		return err
	}
	// Held now, filled later, so nothing waits on a server to draw.
	s.servers = mcppkg.NewInvokers()
	s.sd.servers = s.servers
	opts = append(opts, engine.WithInvoker(s.servers))

	s.bus = event.New()
	s.sd.bus = s.bus
	// Seeded before anything publishes, or a new record lands on an
	// ordinal already on disk. The replay never goes on the bus.
	s.bus.Resume(engine.Resumable(s.restore))
	s.eng = engine.New(s.bus, client, s.tools, s.runners, opts...)
	s.eng.Restore(s.restore)
	return nil
}

// wire connects the subscribers every run has and returns the session's
// ctx, which shutdown cancels first.
func (s *session) wire() context.Context {
	ctx, stop := context.WithCancel(context.Background()) //nolint:gosec // sd.close calls stop
	s.sd.stop = stop

	// Wired before Run so nothing published at startup is missed, and
	// unwatched by shutdown after it drains, so the last record lands.
	// Those doing network or process work take ctx, which shutdown ends
	// first. logging and store take none: they must outlive it.
	s.sd.unwatch = append(s.sd.unwatch, logging.Watch(s.bus))
	if s.events != nil {
		s.sd.unwatch = append(s.sd.unwatch, store.Watch(s.bus, s.events, s.id))
	}
	if s.judge != nil {
		s.sd.unwatch = append(s.sd.unwatch, judgepkg.Watch(ctx, s.bus, s.judge))
	}
	// Without a key there is nothing to compose with, but shipped and saved views still draw.
	s.sd.unwatch = append(s.sd.unwatch, composer(s.cfg.Views, s.judge).Watch(ctx, s.bus))
	s.sd.unwatch = append(s.sd.unwatch, forget.Watch(ctx, s.bus, sessionStore(s.events), s.id,
		forget.WithContainers(containerRemover(sandboxSocketFor(s.cfg)))))

	// After the watchers, so what this records is recorded too.
	announceResume(s.bus, s.id, s.restore, s.env)
	return ctx
}
