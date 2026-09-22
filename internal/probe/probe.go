// Package probe runs a small, fixed, read-only menu once per goal,
// gated by Jev on relevance. Never model-proposed, so it runs unconfirmed.
package probe

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/vitzeno/detent/internal/classify"
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

// Prober runs Menu's probes through one Runner.
type Prober struct {
	runner Runner
}

// New builds a Prober around runner.
func New(runner Runner) *Prober {
	return &Prober{runner: runner}
}

// Each runs probes together and keeps what each said, by name, so a
// caller can gather the whole menu before it knows which of them a
// goal will want.
func (pb *Prober) Each(ctx context.Context, probes []Probe) map[string]string {
	out := make(map[string]string, len(probes))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			said := pb.one(ctx, p)
			mu.Lock()
			out[p.Name] = said
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// Format assembles gathered output into the blob the model reads. The
// one place that shape is written, so output gathered earlier and
// output run just now are indistinguishable to the model, which is
// what makes gathering early safe to do at all.
func Format(probes []Probe, said map[string]string) string {
	if len(probes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Environment context gathered automatically before this goal (not something you ran):\n")
	for _, p := range probes {
		out, ok := said[p.Name]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "\n$ %s\n", p.Command)
		b.WriteString(out)
	}
	return b.String()
}

// one runs a single probe and formats what it said, newline-terminated
// so the blob reads as a transcript.
func (pb *Prober) one(ctx context.Context, p Probe) string {
	res, err := pb.runner.Run(ctx, p.Command)
	if err != nil {
		return fmt.Sprintf("(failed: %v)\n", err)
	}
	out := res.Stdout
	if res.Stderr != "" {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += res.Stderr
	}
	if out == "" {
		return "(no output)\n"
	}
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}
