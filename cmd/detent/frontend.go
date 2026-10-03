package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/engine"
	"github.com/vitzeno/detent/internal/headless"
	mcppkg "github.com/vitzeno/detent/internal/mcp"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
	"github.com/vitzeno/detent/internal/usercommand"
	"github.com/vitzeno/detent/ui"
)

// runHeadless runs the one request -prompt names and prints it.
func (s *session) runHeadless(ctx context.Context) error {
	// Without this a killed -prompt leaves its container behind. Undone
	// before sd.close runs, so a second Ctrl-C during shutdown still kills.
	ctx, untrap := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer untrap()

	for _, w := range s.warnings {
		fmt.Fprintln(os.Stderr, w)
	}
	approve := headless.Approver(nil)
	switch {
	case s.o.unattended:
		approve = headless.AutoDecline
	case s.o.approveAll:
		approve = headless.AutoApprove
	}
	// Subscribed before the engine runs, or SessionStarted is missed.
	printer := headless.New(s.bus, approve, os.Stdout, os.Stderr)
	s.sd.engine = runEngine(ctx, s.eng)
	reason := printer.Run(ctx, s.o.prompt)
	fmt.Printf("\nrequest ended: %s\n", reason)
	if reason == event.EndDone {
		return nil
	}
	return endedError(reason)
}

// runTUI adds what only an interactive session has, the human's own
// shell and MCP, then hands the terminal to Bubble Tea.
func (s *session) runTUI(ctx context.Context) error {
	info := ui.SessionInfo{
		Image: s.cfg.SandboxImage, Mount: s.cfg.SandboxWorkspace,
		Runtime: s.cfg.SandboxRuntime, Network: s.cfg.SandboxNetwork,
		PowerShell: s.env.Shell == model.ShellPwsh,
	}
	// Built before the engine runs: SessionStarted is published once,
	// and a front-end that subscribes afterwards loses it.
	tui := ui.New(ctx, s.bus, info).Restore(s.restore)
	for _, w := range s.warnings {
		s.bus.Publish(event.Notice{Level: "warn", Text: w})
	}
	// The same runner the model's commands go to: a shell that cannot
	// see what the agent just did is not worth having.
	runner, where := s.runners.Select(event.UnknownRisk())
	s.sd.userCommand = usercommand.Watch(ctx, s.bus, runner, where)
	signins := mcppkg.NewSignIns(s.bus, s.servers, mcppkg.Tokens{Dir: mcppkg.TokensDir()}, openBrowser)
	s.sd.unwatch = append(s.sd.unwatch, mcppkg.Watch(ctx, s.bus, s.servers,
		mcppkg.Redialer(s.tools, s.servers, s.configured, signins), signins))
	s.sd.connect = connectServers(ctx, s.bus, s.tools, s.servers, s.configured, signins)
	s.sd.engine = runEngine(ctx, s.eng)

	// Altscreen is declared by ui.Model.View: under Bubble Tea v2,
	// terminal state is a property of what is rendered.
	p := tea.NewProgram(tui)
	// Bubble Tea traps neither, and a closed terminal would otherwise
	// skip every deferred close above.
	hup, unhup := signal.NotifyContext(ctx, syscall.SIGHUP)
	defer unhup()
	go func() { <-hup.Done(); p.Quit() }()
	_, err := p.Run()
	return err
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
