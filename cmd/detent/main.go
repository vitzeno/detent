// cmd/detent runs the full-screen TUI, or one goal headlessly with -goal.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vitzeno/detent/internal/agentloop"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/config"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/ui"
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
	flag.Parse()

	fileCfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	resolved := config.Resolve(fileCfg, *baseURL, *model, *apiKey, *steps)

	if err := propose.Ping(context.Background(), resolved.BaseURL, resolved.APIKey); err != nil {
		return fmt.Errorf("%v\n\nis the model endpoint up? Wanted %s with model %s — for LM Studio, load the model and Start Server; otherwise point -url/-model (or a config file) at your provider",
			err, resolved.BaseURL, resolved.Model)
	}

	sess := &agentloop.Session{
		Proposer: propose.New(
			propose.WithBaseURL(resolved.BaseURL),
			propose.WithModel(resolved.Model),
			propose.WithAPIKey(resolved.APIKey),
			propose.WithHeaders(resolved.Headers),
		),
		Confirm:       confirmHeadless,
		StepBudget:    resolved.Steps,
		RiskThreshold: resolved.RiskThreshold,
	}
	// No judge without a key: TYPESAFE_API_KEY env or jev_api_key file.
	if resolved.JevAPIKey != "" {
		sess.Judge = classify.NewJevJudge(resolved.JevAPIKey,
			classify.WithModel(resolved.JevModel),
			classify.WithEndpoint(resolved.JevEndpoint),
		)
	}

	if *goal != "" {
		return printGoalResult(sess.RunGoal(context.Background(), *goal))
	}
	judgeName := ""
	if sess.Judge != nil {
		judgeName = classify.DefaultModel
	}
	p := tea.NewProgram(ui.New(context.Background(), sess, resolved.Model, judgeName), tea.WithAltScreen())
	_, err = p.Run()
	return err
}

func printGoalResult(res agentloop.GoalResult, err error) error {
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
	return err
}

func confirmHeadless(req agentloop.ConfirmRequest) bool {
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
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(strings.ToLower(line)) == "y"
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
