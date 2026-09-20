// cmd/detent runs the full-screen TUI, or one goal headlessly with -goal.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/config"
	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/resolver"
	"github.com/vitzeno/detent/internal/routing"
	"github.com/vitzeno/detent/internal/sandbox"
	"github.com/vitzeno/detent/internal/usage"
	"github.com/vitzeno/detent/ui"
	"github.com/vitzeno/detent/ui/status"
	"github.com/vitzeno/detent/ui/theme"
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
	model := flag.String("model", "", "model name (default: config file, else "+config.DefaultModel+")")
	apiKey := flag.String("key", "", "API key (default: config file, else env; empty for local LM Studio)")
	configPath := flag.String("config", "", "config file path (default: ./.detent.yaml, then ~/.config/detent/config.yaml)")
	goal := flag.String("goal", "", "run one goal headlessly and exit (empty = launch the TUI)")
	steps := flag.Int("steps", -1, "per-goal step cap, 0 = unbounded (default: config file, else unbounded)")
	themeName := flag.String("theme", "", "color scheme: "+strings.Join(theme.Names(), ", ")+" (default: config file, else "+config.DefaultTheme+")")
	sandboxMode := flag.String("sandbox", "", "sandbox mode: auto, host (default: config file, else auto)")
	sandboxSocket := flag.String("sandbox-socket", "", "containerd socket path (default: config file, else OS-conventional)")
	flag.Parse()

	fileCfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	flagCfg := config.Config{
		BaseURL: *baseURL, Model: *model, APIKey: *apiKey, Theme: *themeName,
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

	if resolved.SandboxMode != "auto" && resolved.SandboxMode != "host" {
		return fmt.Errorf("unknown -sandbox %q — choose one of: auto, host", resolved.SandboxMode)
	}

	if err := propose.Ping(context.Background(), resolved.BaseURL, resolved.APIKey); err != nil {
		return fmt.Errorf("%v\n\nis the model endpoint up? Wanted %s with model %s — for LM Studio, load the model and Start Server; otherwise point -url/-model (or a config file) at your provider",
			err, resolved.BaseURL, resolved.Model)
	}

	proposer := propose.New(
		propose.WithBaseURL(resolved.BaseURL),
		propose.WithModel(resolved.Model),
		propose.WithAPIKey(resolved.APIKey),
		propose.WithHeaders(resolved.Headers),
	)

	sessionID := agent.NewSessionID()
	runners := routing.Selector{Host: host.NewShell(), HostOnly: resolved.SandboxMode == "host"}
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

		container := sandbox.NewContainer(
			sandbox.WithSocket(socket),
			sandbox.WithImage(resolved.SandboxImage),
			sandbox.WithMountPoint(resolved.SandboxWorkspace),
			sandbox.WithRuntime(resolved.SandboxRuntime),
		)
		if err := container.Start(context.Background(), sessionID); err != nil {
			return fmt.Errorf("sandbox: starting container: %w", err)
		}
		defer container.Close(context.Background())
		runners.SandboxRunner = routing.WrapSandbox(container)
	}

	sessOpts := []agent.Option{
		agent.WithID(sessionID),
		agent.WithRunners(runners),
		agent.WithStepBudget(resolved.Steps),
		agent.WithRiskThreshold(resolved.RiskThreshold),
		agent.WithStats(usage.New()),
	}
	// No judge without a key: TYPESAFE_API_KEY env or jev_api_key file.
	if resolved.JevAPIKey != "" {
		sessOpts = append(sessOpts, agent.WithJudge(classify.NewJevJudge(resolved.JevAPIKey,
			classify.WithModel(resolved.JevModel),
			classify.WithEndpoint(resolved.JevEndpoint),
		)))
	}
	sess := agent.New(proposer, headlessConfirmer{}, sessOpts...)

	if *goal != "" {
		return printGoalResult(sess.RunGoal(context.Background(), *goal))
	}

	judgeName := ""
	if sess.Judge != nil {
		judgeName = resolved.JevModel
	}
	// The effective mode, not the configured one: "auto" still reports
	// host when no sandbox ended up wired.
	_, runMode := runners.Select(agent.PreJudgment{})
	info := ui.SessionInfo{
		Proposer: resolved.Model, Judge: judgeName, RunMode: runMode,
		Image: resolved.SandboxImage, Mount: resolved.SandboxWorkspace, Runtime: resolved.SandboxRuntime,
	}
	drv := resolver.New(sess)
	p := tea.NewProgram(ui.New(context.Background(), drv, info), tea.WithAltScreen())
	_, err = p.Run()
	return err
}

func printGoalResult(res agent.GoalResult, err error) error {
	if err != nil {
		fmt.Printf("error: %s\n", err)
	}
	fmt.Printf("goal %q ended: %s\n", res.Goal, res.End)
	for i, c := range res.Commands {
		fmt.Printf("  %d. %s — %s\n", i+1, c.Command, c.Result.Summary())
	}
	if len(res.Commands) == 0 {
		fmt.Println("  (nothing ran)")
	}
	if res.Summary != "" {
		fmt.Printf("summary: %s\n", res.Summary)
	}
	printUsage(res.Stats)
	return err
}

// printUsage renders the goal's measured phases plus session rollups.
func printUsage(g *usage.Goal) {
	if g == nil || len(g.Steps) == 0 {
		return
	}
	fmt.Println("\nusage:")
	for i, s := range g.Steps {
		fmt.Printf("  %d. %s · propose %s/%s · dwell %s · exec %s",
			i+1, s.Command,
			status.Dur(s.Propose), status.Tokens(s.ProposerPrompt+s.ProposerComplete),
			status.Dur(s.Dwell), status.Dur(s.Exec))
		if s.ExitCode >= 0 {
			fmt.Printf("/%d", s.ExitCode)
		}
		if s.HasPost {
			fmt.Printf(" · judge %s/%s", status.Dur(s.JudgePre+s.JudgePost),
				status.Tokens(s.JudgePrompt+s.JudgeComplete))
		}
		fmt.Println()
	}
	fmt.Printf("  goal machine %s · session %s\n", status.Dur(g.MachineTime()), status.Dur(g.Duration()))
}

// headlessConfirmer implements agent.Confirmer for the -goal CLI path:
// read the decision from stdin, same shape as the TUI's own confirm
// modal but rendered as plain text.
type headlessConfirmer struct{}

func (headlessConfirmer) Confirm(req agent.ConfirmRequest) bool {
	fmt.Printf("\ngoal: %q\n", req.Goal)
	if len(req.History) == 0 {
		fmt.Println("done so far: (nothing yet)")
	} else {
		fmt.Println("done so far:")
		for i, h := range req.History {
			fmt.Printf("  %d. %s — %s\n", i+1, h.Command, h.Result.Summary())
		}
	}
	header := "next:"
	if req.Dangerous {
		header = "next — !! LOOK TWICE !!"
	}
	fmt.Printf("\n%s\n  %s\n", header, req.Command)
	if req.Rationale != "" {
		fmt.Printf("why: %s\n", req.Rationale)
	}
	if req.Mutability != "" {
		fmt.Printf("scope: %s\n", req.Mutability)
	}
	if req.RunMode != "" {
		fmt.Printf("runs in: %s\n", req.RunMode)
	}
	if req.Dangerous {
		fmt.Printf("flagged: %s\n", req.RiskNote)
	}
	fmt.Printf("step %d", req.Step)
	if req.StepBudget > 0 {
		fmt.Printf(" of %d", req.StepBudget)
	}
	fmt.Printf(" · goal %d of this session done\n", req.GoalsDone)
	fmt.Print("[y] run   [n] stop goal: ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		// Stdin gone or unreadable: treat as decline, but say so rather
		// than silently falling through — this is the approval gate for
		// potentially dangerous commands.
		fmt.Fprintf(os.Stderr, "detent: reading confirm response: %v (treating as decline)\n", err)
	}
	return strings.TrimSpace(strings.ToLower(line)) == "y"
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
