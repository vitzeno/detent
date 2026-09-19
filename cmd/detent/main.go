// cmd/detent is the entry point. Default mode launches the Bubble Tea
// TUI (§8, §9) — the real front end, goal entered interactively. -cli
// keeps the old synchronous, stdin-confirm print flow from steps 5-8
// around for scripting and debugging: same Loop, same behavior, just
// driven via l.Run instead of the TUI's step-by-step NewRun/Prepare/
// Commit.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/user"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vitzeno/detent/internal/capabilities"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/exec"
	"github.com/vitzeno/detent/internal/extract"
	"github.com/vitzeno/detent/internal/gate"
	"github.com/vitzeno/detent/internal/loop"
	"github.com/vitzeno/detent/internal/reduce"
	"github.com/vitzeno/detent/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "detent:", err)
		os.Exit(1)
	}
}

func run() error {
	loadDotenv(".env")
	apiKey := os.Getenv("TYPESAFE_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("TYPESAFE_API_KEY not set (checked environment and .env)")
	}

	cliMode := flag.Bool("cli", false, "run the plain stdin/print flow instead of the TUI")
	goal := flag.String("goal", "what files are in this directory?", "natural-language goal (-cli mode only)")
	flag.Parse()

	l, err := buildLoop(apiKey)
	if err != nil {
		return err
	}

	if *cliMode {
		l.Confirm = confirmMutation
		return runCLI(l, *goal)
	}
	return runTUI(l)
}

// buildLoop is the wiring shared by both modes: schema, handlers,
// reducers, gate rules, the provider — identical regardless of which
// front end drives it.
func buildLoop(apiKey string) (*loop.Loop, error) {
	domain, err := capabilities.LoadFile("capabilities/unix.yaml")
	if err != nil {
		return nil, err
	}
	capReg := capabilities.NewRegistry()
	if err := capReg.Load(domain); err != nil {
		return nil, err
	}

	execReg := exec.NewRegistry()
	exec.RegisterUnix(execReg)
	if err := capReg.Validate(execReg.Names()); err != nil {
		return nil, err
	}

	reduceReg := reduce.NewRegistry()
	reduce.RegisterUnix(reduceReg)
	if err := capReg.ValidateReducers(reduceReg.Names()); err != nil {
		return nil, err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	currentUser, err := user.Current()
	if err != nil {
		return nil, err
	}

	return &loop.Loop{
		Capabilities: capReg,
		Exec:         execReg,
		Reduce:       reduceReg,
		Judge:        classify.NewJevJudge(apiKey),
		Constructor:  &extract.DeterministicConstructor{},
		Budgets:      loop.DefaultBudgets,
		PathRules:    gate.PathRules{AllowedRoots: []string{cwd}},
		CurrentUser:  currentUser.Username,
	}, nil
}

func runTUI(l *loop.Loop) error {
	ctx := context.Background()
	p := tea.NewProgram(tui.New(ctx, l))
	_, err := p.Run()
	return err
}

func runCLI(l *loop.Loop, goal string) error {
	// l.Run always returns the state accumulated so far, even on a hard
	// error (loop.go: "return run.State(), Termination{}, err") — printing
	// steps taken before returning the error is what makes that visible,
	// instead of silently discarding real progress on a failure.
	state, term, runErr := l.Run(context.Background(), goal)

	fmt.Printf("goal: %q\n", goal)
	if runErr == nil {
		fmt.Printf("terminated: %s", term.Reason)
		if term.Detail != "" {
			fmt.Printf(" (%s)", term.Detail)
		}
		fmt.Println()
	} else {
		fmt.Printf("error: %s\n", runErr)
	}

	for _, f := range state.Findings {
		fmt.Printf("\nstep %d: %s\n", f.Step, f.Action)
		facts, _ := json.MarshalIndent(f.Facts, "", "  ")
		fmt.Println(string(facts))
	}
	if len(state.Findings) == 0 {
		fmt.Println("\nnothing was run.")
	}

	if len(state.Resolved) > 0 {
		fmt.Println("\nresolved:")
		for _, r := range state.Resolved {
			fmt.Printf("  step %d: %s.%s = %v (target_resolvable noul %.2f)\n",
				r.Step, r.Capability, r.Arg, r.Value, r.TargetResolvableNoul)
		}
	}
	return runErr
}

// confirmMutation is the -cli mode's stdin stand-in for §4.3's confirm
// dialog — same data (loop.ConfirmRequest), same rule ("every mutation
// stops, no exceptions"), just plain text instead of the TUI's bordered
// panel (internal/tui/view.go's viewConfirm).
func confirmMutation(req loop.ConfirmRequest) bool {
	fmt.Printf("\ngoal: %q\n\n", req.Goal)
	if len(req.Done) == 0 {
		fmt.Println("done so far: (nothing yet)")
	} else {
		fmt.Println("done so far:")
		for _, f := range req.Done {
			fmt.Printf("  %d. %s\n", f.Step, f.Action)
		}
	}
	fmt.Printf("\nnext — %s:\n  %s — %s\n\n", strings.ToUpper(string(req.Danger)), req.Capability, req.TargetDesc)
	fmt.Printf("write %d of %d allowed · step %d of %d\n", req.WritesUsed+1, req.WriteBudget, req.Step, req.StepBudget)
	fmt.Print("[y] run   [n] stop run: ")

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(strings.ToLower(line)) == "y"
}

// loadDotenv is a minimal .env loader — same discipline as the phase-0
// spike harness (experiments/scripts/spike.py): real environment variables
// always win, .env only fills gaps, and a missing file is not an error.
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
