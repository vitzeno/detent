package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/tool"
)

// Judge is the classifier the jev hook speaks to, declared here at its
// consumer. internal/classify ships the implementation.
type Judge interface {
	Assess(ctx context.Context, command string, threshold float64) (event.Risk, error)
}

// The hooks detent ships, cheapest first.

// toolFloor is what the tool itself declares. read_file is read-only
// by construction, bash is unknown until something else speaks.
type toolFloor struct{}

func (toolFloor) Name() string { return "tool" }

func (toolFloor) Assess(_ context.Context, c tool.Call, _ event.Risk) (event.Risk, error) {
	return event.Risk{Mutability: c.Mutability}, nil
}

// mcpFloor confirms every Call that runs outside the sandbox: no
// checkpoint can undo one, so a human sees each before it happens.
type mcpFloor struct{}

func (mcpFloor) Name() string { return "mcp" }

func (mcpFloor) Assess(_ context.Context, c tool.Call, _ event.Risk) (event.Risk, error) {
	if c.Executor == "" {
		return event.Risk{}, nil
	}
	return event.Risk{Dangerous: true, Note: "mcp: " + c.Executor}, nil
}

// regexHook is the backstop. It only ever adds emphasis, which Widen
// guarantees.
type regexHook struct{}

func (regexHook) Name() string { return "regex" }

func (regexHook) Assess(_ context.Context, c tool.Call, _ event.Risk) (event.Risk, error) {
	for _, p := range dangerPatterns {
		if p.re.MatchString(c.Command) {
			return event.Risk{Dangerous: true, Mutability: p.mut, ScopeRisk: p.scope, Note: p.note}, nil
		}
	}
	return event.Risk{}, nil
}

var dangerPatterns = []struct {
	re    *regexp.Regexp
	mut   string
	scope float64
	note  string
}{
	{regexp.MustCompile(`\brm\s+(-[a-zA-Z]*[rf][a-zA-Z]*\s+)+`), event.MutIrreversible, 0.9, "recursive or forced delete"},
	{regexp.MustCompile(`\b(mkfs|fdisk|dd)\b`), event.MutIrreversible, 0.95, "writes a device directly"},
	{regexp.MustCompile(`\bchmod\s+-R\b|\bchown\s+-R\b`), event.MutSystem, 0.7, "recursive permission change"},
	{regexp.MustCompile(`\b(shutdown|reboot|halt|poweroff)\b`), event.MutSystem, 0.8, "stops the machine"},
	{regexp.MustCompile(`\bkill(all)?\s+-9\b`), event.MutSystem, 0.6, "force kills processes"},
	{regexp.MustCompile(`\bgit\s+(push\s+.*--force|reset\s+--hard|clean\s+-[a-z]*f)`), event.MutIrreversible, 0.8, "discards git history or work"},
	{regexp.MustCompile(`(curl|wget)\b[^|]*\|\s*(ba)?sh`), event.MutSystem, 0.9, "pipes a download into a shell"},
	{regexp.MustCompile(`>\s*/dev/(sd|nvme|disk)`), event.MutIrreversible, 0.95, "writes to a raw disk"},
	{regexp.MustCompile(`\bsudo\b`), event.MutSystem, 0.7, "runs as root"},
}

// repeatHook notices a command run again and again. It cannot stop
// the call, only make it visible.
type repeatHook struct {
	seen  map[string]int
	limit int
}

func newRepeatHook(limit int) *repeatHook {
	return &repeatHook{seen: map[string]int{}, limit: limit}
}

func (repeatHook) Name() string { return "repeat" }

func (h *repeatHook) Assess(_ context.Context, c tool.Call, _ event.Risk) (event.Risk, error) {
	h.seen[c.Command]++
	n := h.seen[c.Command]
	if n < h.limit {
		return event.Risk{}, nil
	}
	return event.Risk{Note: fmt.Sprintf("run %d times already this session", n)}, nil
}

func (h *repeatHook) forget() { clear(h.seen) }

// count reports how many times a command has run, so the Step loop can
// tell the model rather than only the human.
func (h *repeatHook) count(command string) int { return h.seen[command] }

// jevHook asks the classifier what a command would change. The one
// network hook, so it goes last.
type jevHook struct {
	judge     Judge
	threshold float64
}

func (jevHook) Name() string { return "jev" }

func (h jevHook) Assess(ctx context.Context, c tool.Call, _ event.Risk) (event.Risk, error) {
	if h.judge == nil {
		return event.Risk{}, nil
	}
	return h.judge.Assess(ctx, c.Command, h.threshold)
}

// describe renders a Risk for a human, for the approval prompt.
func describe(r event.Risk) string {
	parts := make([]string, 0, 3)
	if r.Mutability != "" {
		parts = append(parts, strings.ReplaceAll(r.Mutability, "_", " "))
	}
	if r.ScopeRisk >= 0 {
		parts = append(parts, fmt.Sprintf("scope %.0f%%", r.ScopeRisk*100))
	}
	if r.Note != "" {
		parts = append(parts, r.Note)
	}
	return strings.Join(parts, ", ")
}
