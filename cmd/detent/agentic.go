package main

import (
	"context"
	"fmt"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/config"
	"github.com/vitzeno/detent/internal/engine"
	"github.com/vitzeno/detent/internal/headless"
	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/routing"
	"github.com/vitzeno/detent/internal/sandbox"
	"github.com/vitzeno/detent/internal/tool"
)

// runPrompt is the agentic path. The TUI still runs the old core
// until it becomes a subscriber too.
func runPrompt(ctx context.Context, prompt string, resolved config.Config,
	container *sandbox.Container, judge *classify.JevJudge, unattended bool) error {

	runners := routing.EngineSelector{
		Host:     host.NewShell(),
		HostOnly: resolved.SandboxMode == "host",
	}
	if container != nil {
		runners.Sandbox = container
	}
	_, runMode := runners.Select(event.UnknownRisk())

	client := &model.Client{
		BaseURL: resolved.BaseURL,
		Model:   resolved.Model,
		APIKey:  resolved.APIKey,
		Headers: resolved.Headers,
		Env:     environment(resolved, runMode == "sandbox"),
	}

	opts := []engine.Option{
		engine.WithSummarizer(client),
		engine.WithContextTokens(resolved.ContextTokens),
		engine.WithMaxSteps(resolved.Steps),
	}
	if judge != nil {
		opts = append(opts, engine.WithJudge(
			classify.RiskJudge{Asker: judge, Threshold: resolved.RiskThreshold},
			resolved.RiskThreshold))
	}

	bus := event.New()
	eng := engine.New(bus, client, tool.Standard(), runners, opts...)
	go eng.Run(ctx)

	approve := headless.Approver(nil)
	if unattended {
		approve = headless.AutoDecline
	}
	reason := headless.Run(ctx, bus, prompt, approve)
	fmt.Printf("\nrequest ended: %s\n", reason)
	if reason == event.EndError {
		return fmt.Errorf("detent: the request failed")
	}
	return nil
}

// environment is what the prompt says about where commands run.
// Describing this process instead puts BSD flags in a Linux container.
func environment(c config.Config, sandboxed bool) model.Environment {
	env := model.LocalEnvironment()
	if !sandboxed {
		return env
	}
	return model.Environment{
		OS: "linux", Arch: env.Arch, Dir: c.SandboxWorkspace,
		Sandboxed: true,
		Network:   c.SandboxNetwork != "none",
		Undoable:  true,
	}
}
