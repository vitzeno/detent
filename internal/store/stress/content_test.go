package stress_test

import (
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/tool"
)

// kind is one tool as the generator uses it. What a tool says about
// itself comes from the registry, so only its content is made up here.
type kind struct {
	name     string
	runner   string
	executor string // what ran a tool call that is not a shell command
	render   event.RenderKind
	renders  event.RenderKind
	args     func(*rand.Rand) map[string]any
	risk     func(*rand.Rand) event.Risk
	result   func(*rand.Rand) event.Result
}

// gen is the made-up half of a kind, keyed by tool name.
type gen struct {
	runner string
	render event.RenderKind // the kind a judge would give its output
	args   func(*rand.Rand) map[string]any
	result func(*rand.Rand) event.Result
}

// registry is what a session offers, so a tool added there without a
// gen here fails TestTools_EveryRegisteredToolHasAGenerator.
func registry() *tool.Registry {
	return tool.Standard(tool.NewSkill([]tool.SkillEntry{{Name: "release", Description: "cut a release"}}))
}

var tools = kinds()

func kinds() []kind {
	reg := registry()
	var out []kind
	for _, name := range reg.Names() {
		g, ok := gens[name]
		if !ok {
			continue
		}
		t, ok := reg.Lookup(name)
		if !ok {
			continue
		}
		spec := t.Describe()
		out = append(out, kind{name: name, runner: g.runner, render: g.render,
			renders: spec.Renders, args: g.args, result: g.result, risk: riskFor(spec.Mutability)})
	}
	// Two MCP tools, which no registry holds until a server answers.
	return append(out,
		kind{
			name: "github__create_issue", runner: "host", executor: "mcp:github", render: event.RendersText,
			args: func(r *rand.Rand) map[string]any {
				return map[string]any{"repo": "vitzeno/detent", "title": pick(r, prompts)}
			},
			risk: mcpRisk, result: issueResult,
		},
		kind{
			name: "docs__search_docs", runner: "host", executor: "mcp:docs", render: event.RendersMarkdown, renders: event.RendersMarkdown,
			args: func(r *rand.Rand) map[string]any {
				return map[string]any{"q": pick(r, queries), "limit": 5}
			},
			risk: mcpRisk, result: searchResult,
		})
}

// riskFor spreads verdicts around what the tool declared, as the chain would.
func riskFor(mutability string) func(*rand.Rand) event.Risk {
	switch mutability {
	case event.MutRead:
		return readOnly
	case event.MutWorkspace:
		return workspaceRisk
	}
	return shellRisk
}

var gens = map[string]gen{
	"bash": {
		runner: "sandbox", render: event.RendersText,
		args: func(r *rand.Rand) map[string]any {
			return map[string]any{"command": pick(r, commands)}
		},
		result: shellResult,
	},
	"read_file": {
		runner: "sandbox", render: event.RendersContent,
		args: func(r *rand.Rand) map[string]any {
			return map[string]any{"path": pick(r, paths)}
		},
		result: fileResult,
	},
	"write_file": {
		runner: "sandbox", render: event.RendersText,
		args: func(r *rand.Rand) map[string]any {
			return map[string]any{"path": pick(r, paths), "content": pick(r, prose)}
		},
		result: wroteResult,
	},
	"edit_file": {
		runner: "sandbox", render: event.RendersDiff,
		args: func(r *rand.Rand) map[string]any {
			return map[string]any{"path": pick(r, paths), "old_string": pick(r, codeLines),
				"new_string": pick(r, codeLines), "replace_all": r.IntN(5) == 0}
		},
		result: diffResult,
	},
	"list_dir": {
		runner: "sandbox", render: event.RendersTable,
		args: func(r *rand.Rand) map[string]any {
			return map[string]any{"path": pick(r, dirs), "all": r.IntN(4) == 0}
		},
		result: listResult,
	},
	"grep": {
		runner: "sandbox", render: event.RendersContent,
		args: func(r *rand.Rand) map[string]any {
			return map[string]any{"pattern": pick(r, []string{"Publish", "func New", "TODO"}),
				"path": pick(r, dirs), "include": "*.go", "ignore_case": false, "max_results": 200}
		},
		result: grepResult,
	},
	"find_files": {
		runner: "sandbox", render: event.RendersTable,
		args: func(r *rand.Rand) map[string]any {
			return map[string]any{"pattern": "*_test.go", "path": pick(r, dirs), "max_results": 200}
		},
		result: listResult,
	},
	"web_search": {
		runner: "sandbox", render: event.RendersMarkdown,
		args: func(r *rand.Rand) map[string]any {
			return map[string]any{"query": pick(r, queries)}
		},
		result: searchResult,
	},
	"skill": {
		runner: "sandbox", render: event.RendersMarkdown,
		args: func(*rand.Rand) map[string]any {
			return map[string]any{"name": "release"}
		},
		result: func(r *rand.Rand) event.Result {
			return event.Result{Stdout: "---\nname: release\n---\n\n" + pick(r, prose) + "\n"}
		},
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
	switch r.IntN(8) {
	case 0:
		return event.Risk{
			Dangerous: true, Mutability: event.MutIrreversible, ScopeRisk: 0.7 + r.Float64()*0.3,
			Note: pick(r, rationales), FromJudge: true,
		}
	case 1, 2:
		return workspaceRisk(r)
	}
	return event.Risk{Mutability: event.MutRead, ScopeRisk: r.Float64() * 0.2, FromJudge: true}
}

func shellResult(r *rand.Rand) event.Result {
	switch r.IntN(12) {
	case 0:
		return event.Result{ExitCode: 1, Stderr: pick(r, failures), Stdout: ""}
	case 1:
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

func diffResult(r *rand.Rand) event.Result {
	if r.IntN(10) == 0 {
		return event.Result{ExitCode: 1, Stderr: "old_string not found"}
	}
	return event.Result{Stdout: fmt.Sprintf("@@ -%d,1 +%d,1 @@\n-%s\n+%s\n",
		1+r.IntN(200), 1+r.IntN(200), pick(r, codeLines), pick(r, codeLines))}
}

func grepResult(r *rand.Rand) event.Result {
	var b strings.Builder
	for range 1 + r.IntN(20) {
		fmt.Fprintf(&b, "%s:%d:%s\n", pick(r, paths), 1+r.IntN(400), pick(r, codeLines))
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
	"2026/09/25 12:07:41 connected docs (15 tools)",
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
