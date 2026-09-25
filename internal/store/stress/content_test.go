package stress_test

import (
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/vitzeno/detent/event"
)

// kind is one tool as the generator uses it, in one entry so a tool
// cannot be half-added.
type kind struct {
	name string
	// executor names what ran a Call that is not a shell command.
	runner   string
	executor string
	render   string
	renders  string
	args     func(*rand.Rand) map[string]any
	risk     func(*rand.Rand) event.Risk
	result   func(*rand.Rand) event.Result
}

var tools = []kind{
	{
		name: "bash", runner: "sandbox", render: "logs",
		args: func(r *rand.Rand) map[string]any {
			return map[string]any{"command": pick(r, commands)}
		},
		risk:   shellRisk,
		result: shellResult,
	},
	{
		name: "read_file", runner: "sandbox", render: "code",
		args: func(r *rand.Rand) map[string]any {
			return map[string]any{"path": pick(r, paths)}
		},
		risk:   readOnly,
		result: fileResult,
	},
	{
		name: "write_file", runner: "sandbox", render: "text",
		args: func(r *rand.Rand) map[string]any {
			return map[string]any{"path": pick(r, paths), "content": pick(r, prose)}
		},
		risk:   workspaceRisk,
		result: wroteResult,
	},
	{
		name: "list_dir", runner: "sandbox", render: "table",
		args: func(r *rand.Rand) map[string]any {
			return map[string]any{"path": pick(r, dirs), "all": r.IntN(4) == 0}
		},
		risk:   readOnly,
		result: listResult,
	},
	{
		name: "web_search", runner: "sandbox", render: "markdown", renders: event.RendersMarkdown,
		args: func(r *rand.Rand) map[string]any {
			return map[string]any{"query": pick(r, queries)}
		},
		risk:   readOnly,
		result: searchResult,
	},
	{
		// Runs in detent's own process, so no checkpoint undoes it.
		name: "github__create_issue", runner: "host", executor: "mcp:github", render: "text",
		args: func(r *rand.Rand) map[string]any {
			return map[string]any{"repo": "vitzeno/detent", "title": pick(r, prompts)}
		},
		risk:   mcpRisk,
		result: issueResult,
	},
	{
		name: "quran__search_quran", runner: "host", executor: "mcp:quran", render: "markdown", renders: event.RendersMarkdown,
		args: func(r *rand.Rand) map[string]any {
			return map[string]any{"q": pick(r, queries), "limit": 5}
		},
		risk:   mcpRisk,
		result: searchResult,
	},
}

func readOnly(*rand.Rand) event.Risk {
	return event.Risk{Mutability: event.MutRead, ScopeRisk: 0.05, FromJudge: true}
}

func workspaceRisk(r *rand.Rand) event.Risk {
	return event.Risk{Mutability: event.MutWorkspace, ScopeRisk: 0.2 + r.Float64()*0.2, FromJudge: true}
}

// mcpRisk is the floor every MCP call gets, whatever the server hinted.
func mcpRisk(r *rand.Rand) event.Risk {
	return event.Risk{
		Dangerous: true, Mutability: event.MutSystem, ScopeRisk: 0.4 + r.Float64()*0.2,
		Note: "runs outside the sandbox, so no checkpoint undoes it", FromJudge: true,
	}
}

// shellRisk spreads: most read, a few write, one in eight needs a human.
func shellRisk(r *rand.Rand) event.Risk {
	switch n := r.IntN(8); {
	case n == 0:
		return event.Risk{
			Dangerous: true, Mutability: event.MutIrreversible, ScopeRisk: 0.7 + r.Float64()*0.3,
			Note: pick(r, rationales), FromJudge: true,
		}
	case n < 3:
		return workspaceRisk(r)
	}
	return event.Risk{Mutability: event.MutRead, ScopeRisk: r.Float64() * 0.2, FromJudge: true}
}

func shellResult(r *rand.Rand) event.Result {
	switch n := r.IntN(12); {
	case n == 0:
		return event.Result{ExitCode: 1, Stderr: pick(r, failures), Stdout: ""}
	case n == 1:
		return event.Result{Err: pick(r, breakages)}
	}
	out := strings.Repeat(pick(r, logLines)+"\n", 1+r.IntN(40))
	return event.Result{Stdout: out, Truncated: r.IntN(30) == 0}
}

func fileResult(r *rand.Rand) event.Result {
	if r.IntN(15) == 0 {
		return event.Result{ExitCode: 1, Stderr: "no such file or directory"}
	}
	var b strings.Builder
	for i := range 8 + r.IntN(60) {
		fmt.Fprintf(&b, "%4d\t%s\n", i+1, pick(r, codeLines))
	}
	return event.Result{Stdout: b.String()}
}

func wroteResult(r *rand.Rand) event.Result {
	return event.Result{Stdout: fmt.Sprintf("wrote %d bytes\n", 200+r.IntN(9000))}
}

func listResult(r *rand.Rand) event.Result {
	var b strings.Builder
	for range 3 + r.IntN(25) {
		fmt.Fprintf(&b, "%s\t%d\n", pick(r, paths), 100+r.IntN(90000))
	}
	return event.Result{Stdout: b.String()}
}

func searchResult(r *rand.Rand) event.Result {
	var b strings.Builder
	for i := range 3 + r.IntN(6) {
		fmt.Fprintf(&b, "## %d. %s\n\n%s\n\n", i+1, pick(r, queries), pick(r, prose))
	}
	return event.Result{Stdout: b.String()}
}

func issueResult(r *rand.Rand) event.Result {
	return event.Result{Stdout: fmt.Sprintf("created issue #%d\n", 100+r.IntN(900))}
}

var prompts = []string{
	"count the go files in this repo",
	"why is the sandbox slow to start",
	"add a test for the compaction path",
	"what changed in the last three commits",
	"the TUI flickers when history is long, find out why",
	"run the linter and fix what it finds",
	"explain how the bus drops events",
	"make the status bar show the context budget",
	"find every place that writes to the store",
	"is the MCP token leaking into the container",
	"benchmark the history pane with a thousand rows",
	"why did that rollback not revert my files",
	"summarise what this package does",
	"upgrade the sqlite driver and check CGO stays off",
	"there is a race in the registry, find it",
	"write a migration that adds a name column",
	"what does the judge do when it times out",
	"trace one call from proposal to render",
	"the windows build broke, work out what landed",
	"check nothing under event imports anything fat",
	"delete the resolver package and fix the fallout",
	"make undo ask before touching my own files",
	"profile the startup and tell me where it goes",
	"what is the oldest session still on disk",
	"document the hook chain in the readme",
}

var prose = []string{
	"Counted them: 214 files, 41k lines, and the biggest is viewspec/widget_table.go.",
	"The delay is the image pull. It only happens on the first run of a new image.",
	"Found it. sizeViewport lays out the whole history on every resize, not just the tail.",
	"That rollback only covers the container. Your own files sit under a bind mount, deliberately outside the snapshot.",
	"Three commits: one renames the hook, one adds the regex backstop, one is a readme edit.",
	"The token never enters the container. MCP calls run in this process, which is the whole reason mcpFloor confirms them.",
	"Nothing imports it any more, so the package can go. I removed it and the build is still clean.",
	"It times out at two seconds and the chain folds the answer as unknown, which widens nothing.",
	"The windows failure is a syscall constant that does not exist there. Guarded it behind a build tag.",
	"Every subscriber has its own queue. A slow one grows it and only drops what says it is lossy.",
	"That is one round trip: an assistant message with three tool_calls and three answers. Indivisible.",
	"Startup is 270ms of endpoint ping and 16s of MCP connect. The ping is already concurrent.",
	"Checked: event pulls in viewspec and google/uuid, and both are stdlib-only underneath.",
	"Added the migration. It applies in one transaction with its version bump, so a half-apply cannot be recorded as done.",
	"The race is the registry map. MCP tools register from a background goroutine now, so it needs the mutex.",
}

var summaries = []string{
	"counted the files", "found the flicker", "fixed the race", "nothing to change",
	"upgraded and verified", "traced the call", "wrote the migration", "explained the chain",
	"removed the package", "benchmarked it", "profiled startup", "documented the hooks",
}

var commands = []string{
	"go test ./... -race", "git log --oneline -20", "ls -la internal/",
	"go build ./...", "gofmt -l .", "rm -rf /tmp/detent-scratch",
	"grep -rn 'Publish' --include='*.go' .", "go vet ./...",
	"wc -l $(git ls-files '*.go')", "git diff --stat", "docker ps -a",
	"go test ./ui/ -run TestApply -v", "du -sh ~/.local/state/detent",
	"sed -i '' 's/old/new/g' internal/engine/hooks.go", "curl -s https://example.com/health",
	"go mod tidy", "git checkout -- .", "find . -name '*.orig' -delete",
}

var paths = []string{
	"internal/engine/engine.go", "internal/engine/turn.go", "ui/apply.go",
	"event/bus.go", "internal/store/store.go", "viewspec/widget_table.go",
	"cmd/detent/main.go", "internal/mcp/connect.go", "internal/sandbox/container.go",
	"logging/events.go", "views/registry.go", "internal/tool/bash.go",
}

var dirs = []string{".", "internal", "ui", "event", "viewspec", "internal/engine", "views"}

var queries = []string{
	"containerd checkpoint lease golang", "bubbletea v2 altscreen",
	"sqlite user_version migration pattern", "go functional options",
	"modelcontextprotocol streamable http", "errgroup shutdown ordering",
	"go test race detector false positive", "lipgloss adaptive colour",
}

var codeLines = []string{
	"func (e *Engine) runTurn(ctx context.Context, t *turnState) {",
	"\tdefer cancel()", "\tif err != nil {", "\t\treturn fmt.Errorf(\"store: %w\", err)",
	"\t}", "", "// Cheapest first, the network hook last.",
	"\te.bus.Publish(event.StepStarted{Turn: t.id, Step: stepID, N: step})",
	"\tselect {", "\tcase <-ctx.Done():", "\t\treturn", "\t}",
	"type Registry struct {", "\tmu    sync.RWMutex", "\ttools map[string]Tool",
}

var logLines = []string{
	"ok  \tgithub.com/vitzeno/detent/ui\t12.228s",
	"--- PASS: TestApply_BuildsABlockPerRequest (0.00s)",
	"level=info msg=\"call started\" tool=bash runner=sandbox",
	"level=warn msg=\"subscriber lagging\" queue=412",
	"2026/09/25 12:07:41 connected quran (15 tools)",
	"pulling image docker.io/library/debian:stable-slim",
}

var failures = []string{
	"exit status 1: no such file or directory",
	"go: updates to go.mod needed; to update it: go mod tidy",
	"FAIL\tgithub.com/vitzeno/detent/internal/engine\t0.327s",
	"permission denied",
}

var breakages = []string{
	"host: context canceled",
	"sandbox: containerd: connection refused",
	"mcp: server closed the connection",
	"host: context deadline exceeded",
}

var rationales = []string{
	"removes a directory tree and nothing undoes it",
	"edits a tracked file in place",
	"checks out over uncommitted work",
	"deletes files matched by a pattern",
}

var notices = []string{
	"2 mcp server(s) ready", "session reset",
	"cannot roll back while a request is running",
	"the judge did not answer in time",
	"context is 72% full",
}

var levels = []string{"info", "info", "info", "warn", "error"}
