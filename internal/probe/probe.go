// Package probe supplies environment context up front instead of
// making the proposer discover it one confirmed command at a time.
// Probes are a small, fixed, read-only menu run once per goal, gated by
// Jev on relevance. Never model-proposed, so they run without confirm —
// the fixed Command field is the whole safety boundary.
package probe

import (
	"context"
	"fmt"
	"strings"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/shell"
)

// Probe is one fixed, read-only context snapshot.
type Probe struct {
	Name         string
	Instructions string // the Jev question: would this help the goal?
	Command      string
}

// Menu is every probe Select can choose from.
var Menu = []Probe{
	{
		Name:         "dir_listing",
		Instructions: "Would seeing the files and directories in the current working directory help answer this goal?",
		Command:      "ls -la",
	},
	{
		Name:         "git_context",
		Instructions: "Would knowing the current git branch and working-tree status help answer this goal?",
		Command:      "git status --porcelain=v1 --branch 2>&1",
	},
	{
		Name:         "process_snapshot",
		Instructions: "Would a snapshot of this user's running processes help answer this goal?",
		Command:      `ps -U "$(whoami)"`,
	},
	{
		Name:         "network_listeners",
		Instructions: "Would knowing what's listening on local network ports help answer this goal?",
		Command:      "lsof -iTCP -sTCP:LISTEN -P -n",
	},
	{
		Name:         "disk_usage",
		Instructions: "Would disk usage of the current directory's immediate subdirectories help answer this goal?",
		Command:      "du -h -d 1 . 2>&1",
	},
}

// fallbackMenu runs with no Judge configured: just the cheapest,
// broadly-useful probe.
var fallbackMenu = []Probe{Menu[0]}

// Judge returns typed judgments; Ask batches one iteration into a
// single call. Identical in shape to agent.Judge, but probe sits below
// agent and can't import it back without a cycle.
type Judge interface {
	Ask(ctx context.Context, state classify.State, questions classify.Questions) (classify.Answers, classify.Usage, error)
}

// Select asks Jev which probes look relevant to goal, one Noul question
// each. A nil Judge or an errored call falls back to fallbackMenu.
func Select(ctx context.Context, judge Judge, goal string) []Probe {
	questions := make(classify.Questions, len(Menu))
	for _, p := range Menu {
		questions[p.Name] = classify.Question{Instructions: p.Instructions, Noul: &classify.NoulQuestion{}}
	}
	answers, _, ok := classify.AskOrFallback(ctx, judge,
		classify.State(map[string]any{"goal": goal}), questions)
	if !ok {
		return fallbackMenu
	}
	var out []Probe
	for _, p := range Menu {
		if a, ok := answers[p.Name]; ok && a.Noul >= 0.5 {
			out = append(out, p)
		}
	}
	return out
}

// Run executes probes via run (shell.Stream in production, or whatever
// the caller injects) and returns one combined blob, or "" when probes
// is empty.
func Run(ctx context.Context, run func(ctx context.Context, command string, onEvent func(shell.StreamEvent)) (shell.Result, error), probes []Probe) string {
	if len(probes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Environment context gathered automatically before this goal (not something you ran):\n")
	for _, p := range probes {
		fmt.Fprintf(&b, "\n$ %s\n", p.Command)
		res, err := run(ctx, p.Command, nil)
		if err != nil {
			fmt.Fprintf(&b, "(failed: %v)\n", err)
			continue
		}
		out := res.Stdout
		if res.Stderr != "" {
			if out != "" && !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			out += res.Stderr
		}
		if out == "" {
			out = "(no output)\n"
		}
		b.WriteString(out)
		if !strings.HasSuffix(out, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}
