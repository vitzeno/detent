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
// checkpoint can undo one, so a human sees each before it happens. A tool
// declared read-only is the exception, since it has nothing to undo.
type mcpFloor struct{}

func (mcpFloor) Name() string { return "mcp" }

func (mcpFloor) Assess(_ context.Context, c tool.Call, _ event.Risk) (event.Risk, error) {
	r := event.UnknownRisk()
	if c.Executor != "" && c.Mutability != event.MutRead {
		r.Dangerous, r.Note = true, "mcp: "+c.Executor
	}
	return r, nil
}

// regexHook is the backstop. It only ever adds emphasis, which Widen
// guarantees.
type regexHook struct{}

func (regexHook) Name() string { return "regex" }

func (regexHook) Assess(_ context.Context, c tool.Call, _ event.Risk) (event.Risk, error) {
	// A read-only tool's command holds what it looks for, and `grep sudo` runs nothing.
	if c.Mutability == event.MutRead {
		return event.UnknownRisk(), nil
	}
	// The unix table too, since a model writes bash in pwsh and Windows has sudo and git.
	if c.Tool == event.ToolPowerShell {
		if r, ok := matchDanger(powershellPatterns, c.Command); ok {
			return r, nil
		}
	}
	if r, ok := matchDanger(dangerPatterns, unquote.Replace(c.Command)); ok {
		return r, nil
	}
	return event.UnknownRisk(), nil
}

func matchDanger(table []danger, cmd string) (event.Risk, bool) {
	for _, p := range table {
		if p.re.MatchString(cmd) {
			return event.Risk{Dangerous: true, Mutability: p.mut, ScopeRisk: p.scope, Note: p.note}, true
		}
	}
	return event.Risk{}, false
}

// unquote drops what sh removes before running a word, so 'rm' and r”m read as rm.
var unquote = strings.NewReplacer(`'`, "", `"`, "", `\`, "")

// danger is one command shape worth a human's eye.
type danger struct {
	re    *regexp.Regexp
	mut   string
	scope float64
	note  string
}

// dangerPatterns read a flag anywhere among a command's words, up to the next
// ;, &, | or line, since rm ./build -rf deletes as surely as rm -rf ./build.
var dangerPatterns = []danger{
	{regexp.MustCompile(`\brm\b[^;|&\n]*\s(-[a-zA-Z]*[rRf][a-zA-Z]*|--(recursive|force))\b`),
		event.MutIrreversible, 0.9, "recursive or forced delete"},
	{regexp.MustCompile(`\bfind\b[^;|&\n]*\s-delete\b`), event.MutIrreversible, 0.9, "deletes what find matches"},
	{regexp.MustCompile(`\b(mkfs(\.\w+)?|fdisk|dd)(\s|$)`), event.MutIrreversible, 0.95, "writes a device directly"},
	{regexp.MustCompile(`\b(chmod|chown|chgrp)\b[^;|&\n]*\s(-[a-zA-Z]*R[a-zA-Z]*|--recursive)\b`),
		event.MutSystem, 0.7, "recursive permission change"},
	{regexp.MustCompile(`\b(shutdown|reboot|halt|poweroff)\b`), event.MutSystem, 0.8, "stops the machine"},
	{regexp.MustCompile(`\b(kill|pkill|killall)\b[^;|&\n]*\s-(s\s+)?(9|KILL|SIGKILL)\b`),
		event.MutSystem, 0.6, "force kills processes"},
	{regexp.MustCompile(`\bgit\s+push\b[^;|&\n]*\s(--force|-[a-zA-Z]*f|--delete|-d|--mirror|\+|:)`),
		event.MutIrreversible, 0.8, "rewrites or deletes a remote branch"},
	{regexp.MustCompile(`\bgit\s+(reset\s+--hard|clean\s+-[a-z]*f|checkout\b[^;|&\n]*\s(--|\.)(\s|$)|` +
		`restore\s+\.(\s|$)|stash\s+(drop|clear)|branch\b[^;|&\n]*\s-D\b)`),
		event.MutIrreversible, 0.8, "discards git history or work"},
	{regexp.MustCompile(`(curl|wget)\b.*\|\s*((sudo|doas|env|exec)\s+)*(\S*/)?((ba|z|da|k)?sh|python3?|perl|ruby|node)\b`),
		event.MutSystem, 0.9, "pipes a download into an interpreter"},
	{regexp.MustCompile(`(^|[\s;&|(])((ba|z|da|k)?sh|eval|source|\.)\s[^;|&\n]*(\$\(|<\(|` + "`" + `)\s*(curl|wget)\b`),
		event.MutSystem, 0.9, "runs a download as code"},
	{regexp.MustCompile(`>\s*/dev/(sd|nvme|disk)`), event.MutIrreversible, 0.95, "writes to a raw disk"},
	{regexp.MustCompile(`\b(sudo|doas|pkexec|run0)\b`), event.MutSystem, 0.7, "runs as root"},
}

// powershellPatterns are PowerShell's, case-insensitive as it is. A
// parameter may be cut to any unambiguous prefix, so -Force is also -fo.
var powershellPatterns = []danger{
	{regexp.MustCompile(`(?i)\b(Remove-Item|rm|ri|rd|rmdir|del|erase)\b[^;|\n]*\s-(r(e(c(u(r(se?)?)?)?)?)?|fo(r(ce?)?)?)\b`),
		event.MutIrreversible, 0.9, "recursive or forced delete"},
	{regexp.MustCompile(`(?i)\b(rd|rmdir|del|erase)\b[^;|\n]*\s/[sf]\b`), event.MutIrreversible, 0.9, "recursive or forced delete"},
	{regexp.MustCompile(`(?i)\b(Format-Volume|Clear-Disk|Initialize-Disk|Remove-Partition|diskpart)\b|\bformat(\.com)?\s+[a-z]:`),
		event.MutIrreversible, 0.95, "formats or wipes a disk"},
	{regexp.MustCompile(`(?i)\b(Stop-Computer|Restart-Computer)\b`), event.MutSystem, 0.8, "stops the machine"},
	{regexp.MustCompile(`(?i)\b(Stop-Process|spps|kill)\b[^;|\n]*\s-fo(r(ce?)?)?\b|\btaskkill(\.exe)?\b[^;|\n]*\s/f\b`),
		event.MutSystem, 0.6, "force kills processes"},
	{regexp.MustCompile(`(?i)\b(iwr|irm|curl|wget|Invoke-WebRequest|Invoke-RestMethod|DownloadString)\b.*\|\s*(iex|Invoke-Expression)\b`),
		event.MutSystem, 0.9, "pipes a download into an interpreter"},
	{regexp.MustCompile(`(?i)\b(iex|Invoke-Expression)\b.*\b(iwr|irm|Invoke-WebRequest|Invoke-RestMethod|DownloadString)\b`),
		event.MutSystem, 0.9, "runs a download as code"},
	{regexp.MustCompile(`(?i)\b(Start-Process|saps|start)\b[^;|\n]*\s-Verb\s+['"]?RunAs\b`), event.MutSystem, 0.7, "runs elevated"},
	{regexp.MustCompile(`(?i)\bSet-ExecutionPolicy\b`), event.MutSystem, 0.7, "changes the script execution policy"},
	{regexp.MustCompile(`(?i)\b(Set-ItemProperty|New-ItemProperty|Remove-ItemProperty|Rename-ItemProperty|Set-Item|New-Item|Remove-Item|sp|rp|ni|si|ri)\b[^;|\n]*\b(HKLM:|Registry::HKEY_LOCAL_MACHINE)|\breg(\.exe)?\s+(add|delete|import|restore)\s+(HKLM|HKEY_LOCAL_MACHINE)\b`),
		event.MutSystem, 0.8, "writes the machine's registry"},
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
	// A delegating call has no effect to judge, so Jev adds nothing, as the
	// regex hook does for a read. Its delegate's own calls are judged.
	if h.judge == nil || c.Delegates || c.Internal {
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
