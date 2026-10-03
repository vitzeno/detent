package engine

import (
	"context"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
	"sync"

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
	r := event.UnknownRisk()
	r.Mutability = event.Declared(c.Mutability)
	return r, nil
}

// mcpFloor confirms every tool call that runs outside the sandbox: no
// checkpoint can undo one, so a human sees each before it happens.
type mcpFloor struct{}

func (mcpFloor) Name() string { return "mcp" }

func (mcpFloor) Assess(_ context.Context, c tool.Call, _ event.Risk) (event.Risk, error) {
	r := event.UnknownRisk()
	if c.Executor != "" {
		r.Dangerous, r.Note = true, "mcp: "+c.Executor
	}
	return r, nil
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
	return event.UnknownRisk(), nil
}

var dangerPatterns = []struct {
	re    *regexp.Regexp
	mut   string
	scope float64
	note  string
}{
	{regexp.MustCompile(`\brm\s+(-[a-zA-Z]*[rf][a-zA-Z]*\s+|--(recursive|force)\b)`), event.MutIrreversible, 0.9, "recursive or forced delete"},
	{regexp.MustCompile(`\b(mkfs(\.\w+)?|fdisk|dd)(\s|$)`), event.MutIrreversible, 0.95, "writes a device directly"},
	{regexp.MustCompile(`\bchmod\s+-R\b|\bchown\s+-R\b`), event.MutSystem, 0.7, "recursive permission change"},
	{regexp.MustCompile(`\b(shutdown|reboot|halt|poweroff)\b`), event.MutSystem, 0.8, "stops the machine"},
	{regexp.MustCompile(`\bkill(all)?\s+-9\b`), event.MutSystem, 0.6, "force kills processes"},
	{regexp.MustCompile(`\bgit\s+(push\s+.*--force|reset\s+--hard|clean\s+-[a-z]*f)`), event.MutIrreversible, 0.8, "discards git history or work"},
	{regexp.MustCompile(`(curl|wget)\b[^|]*\|\s*(sudo\s+)?((ba|z|da)?sh|python3?|perl|ruby|node)\b`), event.MutSystem, 0.9, "pipes a download into an interpreter"},
	{regexp.MustCompile(`>\s*/dev/(sd|nvme|disk)`), event.MutIrreversible, 0.95, "writes to a raw disk"},
	{regexp.MustCompile(`\bsudo\b`), event.MutSystem, 0.7, "runs as root"},
}

// repeatHook notices a command run again and again in one Turn with the
// same output. It only makes that visible, and the Step loop refuses.
type repeatHook struct {
	mu    sync.Mutex
	seen  map[string]repeat
	limit int
}

// repeat is how many runs in a row printed the same thing.
type repeat struct {
	n   int
	out uint64
}

func newRepeatHook(limit int) *repeatHook {
	return &repeatHook{seen: map[string]repeat{}, limit: limit}
}

func (*repeatHook) Name() string { return "repeat" }

func (h *repeatHook) Assess(_ context.Context, c tool.Call, _ event.Risk) (event.Risk, error) {
	r := event.UnknownRisk()
	if n := h.count(c.Command); n > 0 && n >= h.limit-1 {
		r.Note = fmt.Sprintf("ran %d times already this request, printing the same each time", n)
	}
	return r, nil
}

// ran records one run. Output that changed starts the count again,
// since rerunning tests after an edit is the job, not a loop.
func (h *repeatHook) ran(command, output string) {
	f := fnv.New64a()
	_, _ = f.Write([]byte(output))
	sum := f.Sum64()
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.seen[command]
	if r.n > 0 && r.out == sum {
		r.n++
	} else {
		r = repeat{n: 1, out: sum}
	}
	h.seen[command] = r
}

func (h *repeatHook) forget() {
	h.mu.Lock()
	defer h.mu.Unlock()
	clear(h.seen)
}

// count reports how many runs in a row printed the same thing, so the
// Step loop can tell the model rather than only the human.
func (h *repeatHook) count(command string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seen[command].n
}

// refuses reports whether a command has repeated itself enough to stop.
func (h *repeatHook) refuses(command string) (int, bool) {
	n := h.count(command)
	return n, n >= h.limit
}

// jevHook asks the classifier what a command would change. The one
// network hook, so it goes last.
type jevHook struct {
	judge     Judge
	threshold float64
}

func (jevHook) Name() string { return "jev" }

func (h jevHook) Assess(ctx context.Context, c tool.Call, _ event.Risk) (event.Risk, error) {
	if h.judge == nil {
		return event.UnknownRisk(), nil
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
