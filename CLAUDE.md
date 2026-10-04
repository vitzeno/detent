# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

detent is a TUI harness pairing a model with a human at the terminal.
You state a request. The agent calls tools until it has answered,
publishing everything it does on a bus the front-end subscribes to.

There is no closed capability registry. `bash` is one of the tools, so
the model can run anything, and the safety story rests entirely on the
Dangerous flag rather than on structural restriction. A tool call flagged
Dangerous is shown to a human who reads the literal command and must
approve it. Everything else runs straight through.

Dangerous comes from a **hook chain** (`internal/engine/hooks.go`),
cheapest first: each tool's declared mutability, a regex backstop, a
repeat check, then TypeSafe's Jev over the network. The chain folds
every answer with `event.Risk.Widen`, which takes a max and ORs
Dangerous, so **a hook can widen a verdict and never narrow it**:
arithmetic, not a convention. Widening what counts as Dangerous means
registering one more `Assessor`.

## Commands

```sh
make build         # go build -o bin/detent ./cmd/detent
make install       # build, then copy it to go env GOBIN (or GOPATH/bin)
make run           # launch the TUI (go run, no build step)
make test          # go test -race ./..., as CI runs it
make test-fast     # go test ./..., no race detector
make vet           # go vet ./...
make lint          # golangci-lint run, configured in .golangci.yml
make vuln          # govulncheck ./...
make fmt           # gofmt -w over tracked files
make fmt-check     # fail if anything isn't gofmt'd
make tidy-check    # fail if go mod tidy would change anything
```

Run one request headlessly: `./bin/detent -prompt "..."`, with
`-unattended` to decline every flagged tool call instead of asking, or
`-approve-all` to run them all, for a throwaway container like a
benchmark's. Both need `-prompt`. A headless run exits 0 when the request
is done, 1 on an error, 3 at the step bound and 130 when
aborted. Headless runs do not load MCP configuration or connect to MCP
servers. MCP is available in the TUI.
`-init` writes a starting config. `-sessions` lists what can be resumed and `-resume <id>` (or
`-resume last`) continues one.

Run a single package's tests: `go test ./internal/engine/...`
Run a single test: `go test ./ui/ -run TestApply`

Two tests reach outside the process and skip by default:
`go test ./internal/model/ -run TestLive` with `DETENT_LIVE=1` checks
that the endpoint really does tool calling, and the containerd tests
skip themselves when no daemon answers.

CI (`.github/workflows/ci.yaml`) runs `go test -race -cover` on ubuntu
and macos, `go test -cover` on windows (the race detector needs cgo
there), gofmt/vet/`go mod tidy`, lint, viewspec and finder fuzzing and a five-target
cross-build with `CGO_ENABLED=0`. The Windows job is allowed to fail
until it has been green for a while: its PowerShell, Git Bash and Job
Object code had never run on Windows when it was added. The sandbox
talks to containerd over a unix socket, so Windows is host mode only. Keeping `CGO_ENABLED=0` green is why the store uses
`modernc.org/sqlite`, a pure-Go driver. `govulncheck` is not in CI yet:
three containerd 1.7 advisories have no fix short of containerd v2.

## Configuration

Precedence: flags > environment > config file > built-ins. Config file
is `./.detent.yaml` (repo-local, gitignored) or
`~/.config/detent/config.yaml`. `internal/config/detent.example.yaml` names
every key, and is embedded so `detent -init` can write it to the user's
path, 0600 and never over a file already there. As written it sets nothing, so
a config made from it does not pin today's defaults. Relevant env vars: `DETENT_BASE_URL`, `DETENT_MODEL`,
`DETENT_API_KEY` (falls back to `OPENROUTER_API_KEY` then
`OPENAI_API_KEY`), `TYPESAFE_API_KEY` (enables the Jev hook),
`DETENT_CONTEXT_TOKENS`, `DETENT_THEME` (one of `ui/theme.Themes`'
names). The full list is `envKeys` in `internal/config/resolve.go`.
Boolean variables take 1/0, true/false, yes/no or on/off. A `.env` in
the working directory is also loaded at startup, and real env vars
always win over it. An unknown key in the config file is an error that
names its line, so a typo cannot silently do nothing.

`host_shell` (`DETENT_HOST_SHELL`) picks what host commands run in: `sh`,
`pwsh` (PowerShell 7 only, never Windows PowerShell 5.1) or `gitbash`.
Empty picks sh, or on Windows pwsh and then Git Bash, and fails at startup
naming `winget install Microsoft.PowerShell` and Git for Windows. The
sandbox is always Linux sh, and `sandbox_mode: auto` is refused on Windows.

A directory's own `.detent.yaml`, `.env` and `.mcp.json` are read only
once trusted, because each can redirect the key or start a program
before the human has typed anything. The first TUI run there shows what
they set (endpoints, sandbox and log keys, header and variable names,
MCP commands and URLs, never a value) and asks `Trust this directory?
[y/N]`. A yes is recorded in `~/.local/state/detent/trust.json`, keyed
by the directory and a hash of the files, so any change asks again. A
no runs on the user's own config only. Headless runs never ask:
untrusted files are ignored with a warning unless `-trust` is passed,
which trusts them for that run without recording it. What is loaded is
what was hashed: `trust.Decision.Files` carries the bytes `Decide` read,
and `config.Load`, `.env` and `mcp.Load` take the repo-local files from it
rather than the disk, so a file rewritten after the question changes
nothing that run. The record, MCP tokens and logs must be owner-only on
unix (`internal/private`). Windows has no mode bits, so the check passes
there and the profile's ACL keeps them private.

At startup `run()` pings the endpoint's `/models` and fails fast
with a clear message. Don't remove it: it's the difference between a
useful error and a raw dial failure on the first request. It also
applies the theme before building the TUI: `theme.Apply` sets the
active theme, `ui.RefreshStyles` rebakes every style from it. There is
no `init()`: each package bakes its styles from `theme.Current()` as it
loads, and a test fails if a subpackage holding baked styles is missing
from `RefreshStyles`.

### The sandbox VM

Commands run on the host by default (`sandbox_mode: host`), since the
sandbox is experimental. `sandbox_mode: auto` opts in, and needs colima
running with a containerd socket at
`~/.colima/default/containerd.sock`. The runtime does not matter:
colima forwards that socket whether it was started with `--runtime
docker` or `--runtime containerd`.

**Kubernetes must be off for detent's profile.** k3s inside the VM
costs about a core continuously and detent never uses it. Worse, on a
small VM it cannot reach a steady state at all: one seen here had
`cpu: 16, memory: 1` with k8s on, pegged the host at 1094% CPU, and
could not be stopped gracefully because `colima stop` shells in over
ssh and the guest could not answer. `colima stop --force` is the way
out of that.

Sane starting point on a 12-core machine:

```sh
colima start --cpu 6 --memory 8 --disk 30 --kubernetes=false
```

#### Swapping with a project that does want k8s

The cluster lives on the VM disk at `/var/lib/rancher/k3s` and
**survives being disabled**: `--kubernetes=false` stops the service,
it does not reset it. `colima kubernetes reset` is the one that
destroys it.

Two ways round, depending on whether you want both at once:

```sh
# Toggle, one VM, ~30s each way. The cluster comes back as it was.
colima stop && colima start --kubernetes=false   # for detent
colima stop && colima start --kubernetes         # back to k8s
```

```sh
# Or keep them apart: leave the cluster in the default profile and
# give detent its own VM. Nothing to swap, start whichever you need.
colima start --profile detent --cpu 6 --memory 8 --kubernetes=false
```

The second needs detent pointed at that profile's socket, since
`defaultSandboxSocket()` only knows the default one:

```yaml
# .detent.yaml
sandbox_socket: ~/.colima/detent/containerd.sock
```

Two VMs means two disk images and two lots of RAM when both run, so it
buys convenience with resources. `kubectl` follows the profile: the
default one is context `colima`, a named one is `colima-<profile>`.

## The vocabulary

Everything is named for one of five scopes, and using the wrong word
is how a bug gets written. The names are the ones other harnesses and
the APIs use, so a reader coming from them meets nothing new:

| Term | Is | Unit of |
| ---- | -- | ------- |
| Session | process lifetime, one message log | the transcript |
| **Turn** | one human prompt and all the agent did about it | **undo**, the history block |
| **Step** | one model round trip | **the transcript's atom**, compaction |
| **Tool call** | one tool invocation | the row, approval, parallelism |
| **User command** | one command the human ran themselves | looking, not working |

A Step holds zero or more tool calls. A Turn holds Steps until the model
stops asking for tools. Steps are never shown, since a human does not think
in model round trips.

What the model sends is a **tool request** (`event.ToolRequest`, the
wire's `tool_calls` entry, carrying the endpoint's id). Each becomes one
tool call with an id of ours. Two names because they are two things: a
request can be malformed, refused by the step cap or never run, and the
transcript must still answer it.

A **user command** is none of the other four: no model asked for it, so
nothing assesses, approves or judges it, and it opens no Turn. It is
its own noun precisely so it cannot be bolted onto a tool call and quietly
break those three at once, which is why `OutputChunk` and `ViewReady`
carry a `UserCommand` id beside `ToolCall` rather than reuse it.
`internal/usercommand` owns it, and the human starts one in the TUI's
shell mode (shift+tab). Its output is still drawn: `viewgen` asks Jev
its shape, never how it went.

Sessions saved under the old names (Call, Shell) were rewritten by store
migration 0003, so nothing on disk uses them.

### The transcript's atom is a Step

One assistant message carrying N `tool_calls` plus the N `tool`
messages answering them are **indivisible**. Endpoints reject a
`tool_calls` message whose answers are missing, and reject a `tool`
message answering nothing. Three mechanisms depend on it:

- **Abort** must still emit a result for every tool call that never ran, or
  the failure surfaces on the *next* Step, far from its cause.
- **Compaction** moves whole Steps and never half of one.
- **`NoteContext`** lands between Steps, never inside one.

`internal/engine/transcript.go` is the only place that mutates the log,
which is what keeps this true.

### Undo is per Turn

One checkpoint, taken before the first tool call runs, and the Turn is the
only rollback target. Not per tool call: a forty-tool-call Turn would mean forty
containerd snapshots, and "undo call #23" is not a thought anyone has.
Rolling back truncates the transcript to where that prompt landed,
which is a whole number of Steps by construction. Reverting the
human's own files is a separate yes/no question, offered only when the
Turn's checkpoint holds a tree. A Turn already compacted into the
summary cannot be undone: the summary mixes it with what came before, so
undo refuses before touching the sandbox or the files.

## Architecture

**Everything is an event.** 30 facts and 17 intents are the entire
interface between components. Facts are past tense, intents are
imperative, and either may come from anyone: the engine publishes most
facts, but a subscriber answering a question publishes one too. An extension
listens, publishes, or both. There is no second mechanism.

Nothing calls the engine. Every part of the program is one of ten
subscribers:

| Subscriber | Does | Remove it and |
| ---------- | ---- | ------------- |
| `internal/engine` | owns every intent that drives the agent loop | nothing runs |
| `ui` | draws the TUI | the engine still runs |
| `internal/headless` | prints one Turn | the TUI still runs |
| `logging` | the JSONL stream | nothing else notices |
| `internal/store` | the SQLite log, answers `ListSessions` | resume stops, nothing else |
| `internal/judge` | scores a finished tool call, may `SuggestFinish` | rows lose their verdict |
| `internal/viewgen` | composes the view for an output, and draws shipped and saved ones with no key | rows fall back to text |
| `internal/mcp` | answers `ListServers`, signs in on `AuthorizeServer` | `/mcp` draws nothing, no server signs in |
| `internal/forget` | answers `DeleteSession` | `/delete` does nothing |
| `internal/usercommand` | runs `RunCommand`, the human's own | shift+tab stops working |

Each is a `Watch(...)` returning a stop that waits for whatever it
started. One doing network or process work also takes the session's
ctx and derives every request from it, so quitting cancels them all at
once. `logging` and `store` take no ctx on purpose: shutdown stops
everything else first and they must still write the session's last
records.

**The engine is not the only intent subscriber**, and a sentence here
once said it was. Four packages own intents of their own. What holds is
narrower: each intent kind has exactly one owner, and the engine owns
the ones that drive the loop. Disjointness is what makes several
subscribers sound, and it is why cancelling a command is its own
`CancelCommand` rather than a field on `Abort`.

That table is the wiring. `cmd/detent` connects nothing to anything
else, only each part to the bus, which is why each row is one line
there: in `session.wire` for every run, and in `runTUI` for the two
only an interactive session has. `run()` in `main.go` reads top to
bottom as the phases a session opens in (`configure`, `openSandbox`,
`buildEngine`, `wire`, then a front-end), each filling in the
`shutdown` that closes it. Three things follow, and they are the reason the
shape was worth the rework:

- **Tests are events in, state out.** `ui/apply_test.go` drives the
  whole front-end with a slice of `event.Record`: no harness, no
  terminal, no engine.
- **Persistence is replay, not reconstruction.** The store writes
  `event.Record` and resume replays it, so nothing has to reproduce
  what the engine did. `Appended` carries the real messages for
  exactly this reason.
- **A new feature is a subscriber.** Adding one touches `cmd/detent` once
  and no existing component at all.

```
cmd/detent  →  ui, engine, model, tool, classify, config, routing, headless, trust, worktree
ui          →  event, viewspec, views, version, logging, termsafe + its own subpackages
engine      →  event, tool, model, capture, classify (via an interface)
mcp         →  event, tool, capture, the MCP SDK
forget      →  event (the store and sandbox arrive as arguments)
usercommand →  event, capture (the runner arrives as an argument)
tool        →  event
model       →  event
event       →  the standard library, plus viewspec and google/uuid
viewspec    →  the standard library, nothing else
termsafe    →  the standard library, nothing else
gitroot     →  the standard library (instructions and skills share it)
private     →  the standard library, nothing else
views       →  viewspec, event
logging     →  the standard library, plus event
config      →  engine, model, classify, sandbox, host (for defaults and names only)
host        →  capture, winjob
sandbox     →  capture (never host or engine)
routing     →  engine, sandbox
```

`ui` imports **nothing** under `internal/`. That used to need a
translation layer (`internal/resolver`) mirroring every type. Now both
sides import `event` directly and the layer is gone. `event` earns
that by depending on almost nothing, and the rule is a property rather
than a list: **every non-stdlib package `event` imports must itself
import only the standard library.** `viewspec` and `google/uuid` both
qualify. `event/event_test.go` walks the imports and checks it, so
adding a fat dependency fails with the transitive import named.

- **`event`**: the shared vocabulary and the `Bus`. `Publish` never
  blocks, whoever is listening and however slowly, so publishing from
  inside a handler is safe and cannot deadlock. Each subscriber has
  its own queue: a lagging one grows it and drops only events that say
  they are `Lossy`, which is `OutputChunk` and nothing else. A dropped
  live line costs a redraw. A dropped `ToolCallEnded` is a row that never
  finishes. `Record` carries a gapless `Ordinal`, and every subscriber
  receives records in ordinal order, so live order and replay order
  agree and a subscriber that filters or drops can be told apart from
  one that lost something. `Bus.Handle` subscribes a function rather
  than a channel, and `Settle` then waits until it has returned, which
  is what a test needs to mean "processed" rather than "received". Ids are `uuid.UUID` directly, with no
  wrapper type: `google/uuid`'s v7 is monotonic within a millisecond
  as well as across them, which matters because a Step mints all its
  tool call ids inside one.

- **`internal/engine`**: the loop, and it drives itself. One
  goroutine, blocking and linear, reading intents and publishing
  facts. `Run` is the actor and `runTurn` reads top to bottom. That is
  what lets front-ends subscribe rather than call, and why there is
  one loop instead of a blocking one for headless and a shattered one
  for the TUI. `New` subscribes to intents, not `Run`, so a caller
  that publishes the moment it returns cannot lose the intent.
  `Abort` is handled in `dispatch` rather than queued to the Turn: a
  blocked tool call never reaches a boundary, and the inbox is only drained
  at one. Intents name their Turn and one naming another is dropped,
  so a late verdict cannot stop the next request. Contiguous tool calls
  whose tool declares them read-only run concurrently, and anything
  else runs alone in the order asked: parallelism comes from the tool,
  never from a hook, so a judge saying "reads only" cannot make `bash`
  parallel. The repeat check counts runs per Turn that printed the
  same thing, so rerunning tests after a fix is never refused. Every tool call is bounded by `commandTimeout`
  (10m, `command_timeout`), host and sandbox alike, and one stopped by
  it says how long it ran. A declined tool call returns a result saying
  so and its siblings still run: **declining stops a tool call, not a
  Turn.** A reply with no calls that stopped short (cut off, errored,
  empty or only reasoning) is nudged to carry on, twice at most, and a
  Turn that ran anything not read-only is asked once to check its work
  against the request before it ends (`finish_check`). The judge reading the request
  as answered is advice the model reads at its next Step (`SuggestFinish`),
  never a stop: when it ended Turns, every such stop in real sessions cut
  the model off mid-plan. `MaxSteps` defaults to 100 and is soft: hitting it publishes
  `BoundReached` and waits, because a human is watching and stopping
  dead is worse than asking. Undo (`RolledBack`) is a fact, so a resumed
  session replays it rather than bringing back what the human threw away.
  `/new` starts another session under a new id: the engine publishes its
  `SessionStarted`, the store, log and `forget` follow it, and the old
  session is left whole and resumable. `SessionReset` is only read from
  sessions stored before that. A crash's end is a fact too: a
  resumed session's `Run` ends whatever the old process left open (its
  Turn, tool calls and the human's command) with ordinary facts the store
  records, and tells the model its last request was cut off.

- **`internal/tool`**: the closed set a model may call, each lowered
  to one shell command so the sandbox stays the only executor of the
  sandbox. `Lower` is pure: `args → command`, which is why that layer
  tests with no I/O. On the host, a tool that is also `Native` (every
  file tool, `skill` and `web_search`) runs its own `Run` in this
  process instead, the same Go on every OS, so no host needs a shell
  dialect for them. Parity tests run both sides in one directory and
  require the same output and exit code, since the model reads
  whichever ran. The deliberate gaps: the host's grep is RE2 (no
  backreferences), and `list_dir` has its own format because `ls -l`
  differs by OS. A native tool honours its ctx, so the command timeout
  and Abort stop it, holds a bounded amount of any one file (a line is
  kept to the output budget and the rest skipped, edits refuse files over
  50MB), and refuses a pipe, socket or device rather than block on it.
  When the host shell is pwsh the shell tool is
  `powershell` rather than `bash` (`tool.StandardFor`), so a model
  asked for PowerShell does not write bash, and it is shown literally
  like bash. The shell tool is not privileged: same registry, same schema, same
  hook chain. If it ever needs a code path the others don't have, the
  registry is wrong. Every bad call comes back as a **tool result**,
  never a Go error: an unregistered name, arguments failing the
  schema, a hallucinated parameter. The model reads it and corrects
  itself, which is the entire point of a loop. Under `strict: true`
  every property must appear in `required`, so an optional parameter
  is nullable rather than omitted, found by running it, not by
  asserting on it. `web_search` is what proves the rule holds even for
  the network: in the sandbox it is one curl against a **keyless** engine, so
  there is no API key to put in the container, in the command, or in
  the log. It reads results through `r.jina.ai`, so two third parties
  see every query, and a query is capped at 256 bytes. Reading a result stays an ordinary curl from the shell tool, which is the
  distinction worth keeping: searching is the harness reaching out,
  fetching is a command, and only the second goes through flagging.

- **`internal/model`**: the tool-calling client. `Complete` is one
  Step. The transcript's own types (`Message`, `ToolCall`, `Role`)
  live in `event`, not here: `event.Appended` carries them and `model`
  imports `event` already, so the other direction is a cycle. Same
  reasoning as `event.Usage`. A call whose `arguments` will not parse is **kept**, with
  `Err` set: the assistant message already named that id, so dropping
  it leaves the transcript owing an answer. `Environment` is what the
  prompt says about where commands run. Describing this process while
  they run in a container is how BSD flags end up in a Linux one, and
  `Environment.Shell` names the shell so the prompt's rules match it. A
  429 or 5xx is retried twice, honouring `Retry-After`, and `Ping`
  sends the same key and headers `Complete` does.

- **`internal/capture`**: the bounded-output primitives every backend
  shares: `Result`, `StreamEvent`, `MaxOutputBytes`, `ScanCapped`
  (which says when it truncated) and `Clip` (head and tail, on a rune
  boundary). The caller of a Runner closes its output channel once
  `Run` returns, never the Runner. No `exec.Cmd` or containerd
  knowledge of its own.

- **`internal/host`**: runs a command on this machine through sh,
  PowerShell 7 or Git Bash (`dialect.go`). pwsh gets its script as
  `-EncodedCommand`, with a prelude for UTF-8 output and no progress
  bars, and a trailer that exits as sh would. Not the only `exec.Command`: a stdio MCP server, the
  worktree's git and opening a sign-in link each start their own
  process. A non-zero exit is a `Result`, not an error. A command runs
  in its own process group, or on Windows a Job Object
  (`internal/winjob`), so a timeout or abort kills its children too,
  while a backgrounded child outlives a command that finishes. detent's
  own API keys are removed from its environment. The regex backstop in
  `engine/hooks.go` has a PowerShell table, applied to `powershell`
  calls before the unix one.

- **`internal/sandbox`**: `Container`, a session-scoped containerd
  Runner, one per session so filesystem state accumulates. Imports
  `capture`, never `host` or `engine`, which is what lets it satisfy
  `engine.Runner` and `engine.Snapshotter` **structurally**, with no
  adapter needed, since its checkpoints were already plain strings.
  Output is captured by shell-redirecting into files and polling them
  (`tail.go`), not containerd's FIFO streaming: a FIFO needs the shim
  and the reader on the same kernel, which stops holding once the
  daemon runs inside a VM. **`Run` is serialised** by a slot that
  also covers `Snapshot` and `Rollback` and gives up with its ctx: one
  task and one spec per container, so parallel tool calls overwrote each
  other's and returned exit 0 with no output. Real parallelism needs
  one long-lived task and `task.Exec` per tool call. **A rollback does not revert the
  workspace**: that is a bind mount to the user's real directory,
  deliberately outside the snapshot. Checkpoints are held by a
  per-session lease. Without one the GC sweeps them and rollback works
  exactly once. Only `Close` deletes a container and its lease, and the
  ids are per session, so a killed process leaves leftovers its own
  resume would collide with: `clearStale` removes them at startup, and
  `Prune` (behind `-prune`) does the same for sessions nobody resumes.
  Both refuse a container whose task still runs, or whose holder label
  names a live process on this host, which is another detent holding
  that session rather than a leftover. A cancelled task is killed and
  waited for on a context the cancel does not end, or the next
  `NewTask` fails "already exists" for the rest of the session.

- **`internal/worktree`**: checkpoints the human's own directory,
  which the container snapshot never covers. `Dir` satisfies
  `engine.Worktreer` structurally and is wired in the TUI only when the
  working directory is inside a git work tree, in host and sandbox mode
  alike. Git plumbing against an index of its own (`GIT_INDEX_FILE`),
  kept for the session so its stat cache spares a rehash, so it captures
  tracked *and* untracked files without touching the index, branch or
  stash, and `.gitignore` is honoured for free (the sandbox's
  `.detent-sandbox/` ignores itself). That index is **never seeded from
  the human's**: theirs holds git's cleaned blobs (LF under autocrlf, a
  pointer under git-lfs), and a restore runs with every filter off, so a
  seeded checkpoint wrote those over the human's files. It runs at the work tree's root, scoped to the starting
  directory. The engine checkpoints again as a Turn ends, so a revert
  leaves alone anything changed after it and says so. Checkpoints are
  unreferenced trees, so `git gc` eventually prunes them and an old one
  reports `ErrGone`.

- **`internal/classify`**: `JevJudge`, the HTTP adapter, and
  `RiskJudge`, which adapts it to the engine's hook chain. It answers.
  It never decides, because `Widen` folds its answer with everyone
  else's. A failed request is an error the engine shows once a Turn,
  and adds nothing to the verdict. With a key set, every tool call's request,
  command and up to 4KB of its output go to TypeSafe, whatever
  `log_bodies` says.

- **`internal/mcp`**: tools an MCP server holds, so a credentialed
  service can be called without its credentials entering the sandbox.
  A server is launched over stdio or reached over Streamable HTTP. The
  SDK takes no headers, so a bearer token rides on a client of ours.
  Servers are configured in `.mcp.json`, not `.detent.yaml`, because
  that file and its `mcpServers` shape are what every MCP client reads
  so one written for another client works here unchanged. The user's
  and the project's are merged nearest-last, an entry at a time rather
  than field by field, and `${VAR}` expands from the environment so a
  committed file can name a token it does not hold.
  MCP tool calls run in this process, which is why the sandbox is the only
  executor **of shell commands** rather than of everything. Nothing a
  checkpoint can undo, so `mcpFloor` confirms every one: `Widen` makes
  that stick, since a server's own `readOnlyHint` can only widen a
  verdict. A tool arrives as `server__name`, sanitised to what an
  endpoint accepts, and a built-in always wins a collision:
  `tool.Registry.Register` refuses to replace one. A sign-in token is
  saved under the server's name and URL, so a project file reusing a
  name for another URL never receives it, and configured headers go
  only to the configured origin. Its schema
  is passed through rather than rebuilt, so those tools are not offered
  as `strict` and `Prepare` leaves their arguments to the server, which
  the spec says must validate them anyway.

- **`internal/trust`**: decides, before config, `.env` or MCP are read,
  whether the working directory's own files may be. It reads each file
  once, summarises and hashes those bytes, and hands them on in
  `Decision.Files`, which is all the loaders read. Its summary never sees
  an expanded secret.

- **`internal/instructions`**: the project's `AGENTS.md`, or `CLAUDE.md`
  where a directory has none, from the git root down, after the human's
  own `~/.config/detent/AGENTS.md`. Read once on the host at startup, so
  the sandbox changes nothing, and appended to the built-in prompt rather
  than replacing it. `SessionStarted.Instructions` names what was read.
  Capped at 128KB (about 32k tokens), outermost file cut first. The cap
  is fixed, not scaled to `context_tokens`, so a small window feels it.
  A project file that links outside the repository is refused, since
  this read sits outside the sandbox, and one unreadable file no longer
  drops the others.

- **`internal/skills`**: Agent Skills, found in `.agents/skills` and
  `.claude/skills` from the git root down, then the human's own, then the
  ones detent ships (`builtin/`, embedded and written to
  `~/.local/state/detent/skills` so the tool and a sandbox mount can read
  them as files, searched last so the human's own of a name wins). Parsing
  is lenient, as the standard's guide says. `tool.Skill` loads one by
  lowering to a read of its `SKILL.md`, not through the invoker, since
  `mcpFloor` would flag every load. Its `name` is an enum, so under strict
  mode the endpoint refuses a skill that does not exist. In the sandbox a
  skill outside the working directory is a read-only mount
  (`sandbox.WithReadOnly`). A project skill that links outside the
  repository is skipped with a warning.

- **`/context`** is measured by the engine, the only part that sees a
  whole request. `ContextMeasured` is published after every Step, scaled
  so its parts add up to the endpoint's real `PromptTokens`, once at
  startup, and on `MeasureContext`, which `dispatch` answers even
  mid-Turn since it only reads under the transcript's lock. The
  transcript marks where each request starts so history is split by
  request. Nothing is listed by hand: `model.Client.PromptParts` names
  each piece of the system prompt (a test fails if they stop adding up to
  it), and each tool's `Spec.Group` names its row, so a new prompt piece
  or a new kind of tool shows up labelled with no change to `measure`.

- **`internal/forget`**: what a deleted session leaves: its events,
  and the container nothing will resume. The log stays, since a
  diagnostic outliving the thing it describes is the point of one. The
  store and the container remover are taken rather than imported, so
  the one path that destroys things tests with nothing to destroy. The
  container goes first, so a session another detent holds is refused
  before any event is deleted, and the running session is refused: its
  store and container are both open.

- **`internal/headless`**: one prompt on a terminal, no TUI. A bus
  subscriber like any front-end, which is what makes it a fair test of
  the engine's interface. `New` subscribes a `Printer` before the
  engine runs, so it misses nothing, and `Run` sends the prompt.
  Everything it prints that a model or command could influence goes
  through `termsafe.Printable`, as the approval box does, so an escape
  sequence is shown rather than sent to the terminal.

- **`internal/viewgen`**: writes a spec by asking the judge closed
  questions and assembling the answers, rather than asking a model to
  write JSON. That path existed, ran 31s median against 300ms, and
  could name a widget, a role or a field that did not exist. All three
  happened. None is representable from a list the program built.
  Without a key it still draws shipped and saved views (`Unjudged`),
  and `views: generate` sends a user command and up to 4KB of its
  output to the judge.

- **`ui`**: the TUI, and nothing but a projection of the event
  stream. `SessionStarted` is the one description of a run: model,
  sandbox, network, step bound, whether anything is recording it and
  how much it resumed from. `ui.SessionInfo` carries only what no
  fact does, so the panes and the log cannot disagree about what ran. `facts.go` folds facts in and is the one place it learns
  anything. `Model.send` is the one place it asks for anything, and
  `intents.go` holds most of its callers. Seven `tea.Cmd` constructors and eight message types
  collapsed to one of each, so a test drives it with a sequence of
  events and no harness at all. `ui/doc.go` is the file map and the
  naming rules. Read it before adding a file.

  **Drawing costs what is on screen, not what the session has done.**
  Five things hold that. Tests in `render_test.go` catch a regression
  and `ui/bench_test.go` measures it:
  `nextFact` coalesces facts over a 2ms window, `sizeViewport` lays
  history out once and `View` reads what it left, only the tail is
  drawn while following, each block caches its drawing under
  `blockKey`, and the output pane skips a redraw under `detailKey`.

  Two rules come with that. A block must render from its own state
  alone, since `historyTail` may never visit the ones before it, and
  a whole-history pass belongs in `historyAll`. A cache key must name
  every input its content depends on: miss one and the pane renders
  stale, which is why `blockKey`, `histKey` and `detailKey` are
  mutation tested. Each block keeps its own revision, so a live line
  redraws its block and no other.

  **The approval box is the safety story's one screen.** Control and
  escape characters in anything the model wrote are shown, never sent
  to the terminal, and a command taller than the box shows a "more"
  marker and is not approved until its last line has been on screen.
  A question raised while the human is in something they opened (the
  finder, undo, delete) or has typed into the bar waits until that is
  closed or sent, so a key meant for it can never answer the question.
  Once up, a question ignores answers for 400ms (`questionSettle`), so
  the first key of the next message is not taken as a yes. A command's
  output is defused once, as `facts.go` folds it (`termsafe.Styled`): its
  colour stays, and any other escape (a cursor move, a clipboard write, a
  link) is shown rather than sent, so no pane or preview has to remember to. A view that panics while drawing
  falls back to plain text.

  **A thing leaves `ui` when it stops needing Model.** That is why
  `island`, `layout`, `markdown`, `search`, `status`, `theme` and `welcome` are
  subpackages and nothing else is: they take values and return
  strings. The compiler enforces it, since a subpackage importing
  `ui` would be an import cycle. Rendering could go the same way once
  Model's state is passed to it as values. Worth doing if a second
  front-end ever wants the same drawing, not for one.

- **`viewspec`**: the view interpreter, outside `internal/` and
  stricter than anything else: it imports **only the standard
  library**, enforced by `TestPackage_DependsOnStdlibOnly`. A `Spec`
  says how to read a command's output (`Parse`) and how to draw what
  was read (`Blocks`, over a closed widget vocabulary). Ten parse
  kinds and thirty widgets, each widget in its own `widget_*.go`. Numbers are
  read by `number`, not `strconv.ParseFloat`, which rejected every
  column `df` prints: `45%`, `1.2G` and `1,024` all came back 0 and
  drew an empty bar rather than an error anyone could see. Three calls
  priced by frequency: `Compile` once per spec, `Bind` once per
  output, `Draw` per frame, though a few widgets still read numbers
  and times as they draw. `Painter` is on `Frame`, not `Compiled`, so
  the first two are pure data and test with no styling at all. `Bind`
  leaves `Compiled` untouched, so one may be bound from several
  goroutines, and no drawn line is wider than `Frame.Width`. Ordinary
  output never panics: every extractor and `number` has a fuzz target
  in `fuzz_test.go`.
  `Frame.Height` says how tall the pane is for widgets that can grow
  into it and **clips nothing**, because clipping is what would stop a
  long view scrolling. A widget must implement `Widget`. `Validator`,
  `Selector`, `Described` and `Container` are optional and found by
  type assertion. `Selector` is load-bearing: only a kind that can say
  which line the cursor is on may carry `on_enter`, because accepting
  it elsewhere drew a spec that looked right and did nothing when the
  human pressed enter.

- **`logging`**: the structured log, importing only the standard
  library and `event`, so anything but `event` may import it
  (`TestPackage_ImportsOnlyStdlibAndEvent`). Files are owner-only. **One JSONL stream per session**, never one file per
  component: the unit anyone investigates is a step, and a step
  crosses four or five components, so splitting by component would
  make filtering easy and correlating impossible. `events.go` is the
  closed vocabulary. An event name is a record's primary key, since a
  query cannot match free text reliably. `Body` withholds prompts,
  replies and output unless `log_bodies` is set, and free text such as
  a notice or an error is cut to a snippet.

- **`views`**: every spec detent ships, keyed by command name
  (`ForCommand`) and by judged output shape (`ForKind`, an
  `event.RenderKind`). Imports `viewspec` and `event` and nothing else,
  which is what lets both `ui` and `viewgen` read it without either
  importing the other. Result statuses and render kinds are typed
  constants in `event/verdict.go`, and judge, viewgen, views and
  `ui/status` each have a test that their copy agrees with it.

- **`internal/store`**: a session's events on disk, so it can be
  replayed rather than reconstructed. Speaks `event.Record` and knows
  nothing about the engine. A `sessions` header and an append-only
  `events` log with a foreign key between them: the header is written
  once from `SessionStarted` so it cannot drift, while a `turns` table
  would be a mutable aggregate over several facts and would. The
  schema lives in `migrations/*.sql`, embedded, plus Go steps in
  `goSteps` for what SQL cannot say (rewriting payloads), numbered
  together and versioned by SQLite's own `PRAGMA user_version` rather
  than a migration library. Each step applies in one transaction with
  its version bump, so a half-applied one cannot be recorded as done,
  and a database about to migrate is first copied to
  `events.db.bak-v<n>` with `VACUUM INTO`. A Go step is frozen once
  released: it carries its own copy of the shapes it reads rather than
  the event package's, which will have moved on. Each session row says
  which detent started it and the schema its records are in. Encoding is **not** here: a
  store holding its own type list would decode every old record and
  silently drop a new one, so `event/codec.go` owns it and a test
  parses the package to prove no type lacks a codec. `Watch` is the
  subscriber, wired beside `logging.Watch`. It skips `OutputChunk`
  because a replayed tool call has already finished and `ToolCallEnded` carries
  the whole output. It also answers `ListSessions` over the bus, since
  `ui` cannot import it to ask directly. Its pragmas (`foreign_keys`,
  `busy_timeout`, WAL) are in the DSN and the pool holds one connection,
  because a pragma set by `Exec` reaches one pooled connection and a
  delete's cascade silently skipped the rest. WAL leaves `-wal` and
  `-shm` files beside `events.db`.

- **`version`**: what this build calls itself, and nothing else.

### Changing what an event stores

Every stored fact's fields carry a `json` tag, so the key on disk is a
decision rather than whatever the Go field is called, and
`event/testdata/shapes.golden` lists every key path each kind stores.
Renaming a Go field is free. Changing a tag, a kind string, or a field's
type fails `TestShapes_StoredFactsKeepTheirShape`, and the fix is two
things in one commit: a store migration that rewrites the old records
(see `internal/store/migrate_0003.go`), then
`go test ./event -run TestShapes -update`. Adding a field needs only the
update, since an old record decodes it as zero.

### Adding or changing a widget

Four files can decide something about a widget, and it is worth knowing
which, because three of them fail *silently* when missed. Adding
`markdown` needed all four and had only one, for months.

| Decides | Where | Missed it? |
| ------- | ----- | ---------- |
| the widget exists and draws | `viewspec/widget_<name>.go` | compile error |
| what it is for, and whether it summarises | its own `Describe()` | **absent from every choice list** |
| which output shapes may use it | `internal/viewgen/kinds.go` | **never offered** |
| anything needing more than stdlib | registered in `ui/spec.go` | n/a |

`Describe()` is optional so a consumer can register a plain function as
a widget, which is right. The cost is that a widget without one is
registered, drawable, and invisible to the judge with no error
anywhere. `Summarises` lives there too, rather than in a list in
viewgen, so registering a widget is enough to have it offered as a
summary: the fact belongs beside the widget, not somewhere that has to
be kept in step with it.

The fourth row is `markdown`: it needs glamour, which is 126 packages,
and `viewspec` imports only the standard library
(`TestPackage_DependsOnStdlibOnly`). So `ui` registers it over the
standard set. That is the extension point working as intended, and it
is safe in one direction only: `ui` adds to `viewspec.Standard()` and
never removes, so the composer can only pick what `ui` can draw.
`TestRegistry_ComposerCannotPickWhatUiCannotDraw` pins that.

`ui`'s `markdown.Wants` heuristic is not a fifth place. It is the same
two-layer split `render_kind` already has: a heuristic when no judge
has spoken, the judge's answer when one has. `fallbackChain` runs
before anything is judged and with no Driver at all.

### Adding a new feature

Start from what the feature actually is:

- **Pure UI** (a keybinding, a different rendering of data an event
  already carries): `ui` only. Drive `apply` with events, no harness
  is needed.
- **Pure engine** (a new hook, a bound, a loop rule):
  `internal/engine` only, with its own tests.
- **A new tool**: `internal/tool`, one file, a `Spec`, and a `Lower`.
  Nothing else changes.
- **Anything crossing the boundary** (new data a front-end must see):
  add the field to the fact in `event`, publish it in `engine`, fold
  it in `ui/facts.go`. Three edits, each provable on its own, and the
  compiler catches the first two.

A tool that knows how its output should be read says so in
`Spec.Renders`, which rides on `ToolCallProposed` and beats a judged
render kind, because the tool knows and the judge is estimating. No
shipped tool claims one: `web_search` returns markdown but glamour
prints every link's destination, and DuckDuckGo's redirects double the
output.

There is no DTO mirror to keep in step any more. The thing that
replaced it is the rule that `event` may import nothing but the
standard library and `viewspec`. Break that and the boundary is back.

## Conventions

- Commit messages: short and concise, no body, no references to plan
  documents or section numbers.
- `docs/` is gitignored: planning documents live there but are never
  committed to the repo.
- Confirm is conditional on `event.Risk.Dangerous`, not universal. A
  tool call nobody answers blocks its Turn rather than running, which is
  the right way round: the approval gate fails closed.

## Go style

The rules the code already follows, so new code reads like the old.

### Building things

- **A type with behaviour or dependencies is built through `New...`.** A
  client, a judge, a store, a generator, a selector: `model.NewClient`,
  `viewgen.New`, `classify.NewRiskJudge`. Its fields are unexported, so a
  literal in another package cannot skip the defaults. Plain data stays a
  literal: events, `capture.Result`, viewspec specs, `config.Config`.
- **Required values are arguments, optional ones are options.** An option
  is `type XOption func(*X)` with constructors named `WithY`
  (`model.WithHeaders`, `engine.WithMaxSteps`), applied in order, and a
  switch that only turns something on reads as one (`viewgen.WithoutVerdicts`).
  Several values that belong together go in one option (`WithInstructions(text, files)`).
- **Defaults are applied once, in the constructor**, after the options, with
  `cmp.Or` where it fits (`NewClient`), never re-derived on every call by an
  accessor.
- **A choice between variants is a function**, not a literal at the call
  site: `tool.Shell(pwsh)`, `routing.Host(r)` and `routing.Sandbox(r)`. The
  zero value is a safe default where one exists (a zero `routing.Selector`
  refuses every call rather than running on the host).

### File layout

- **Important things first.** The package doc, then constants, package
  variables and types, then the exported API and the main flow in the order
  it happens (open, handle, close), then helpers, with small pure functions
  last. A reader should meet what the file is for before how it does it.
- **A type's constructor and options come straight after the type**, and its
  methods after those. A helper type another type depends on sits beside it.
- **Tests come first in a test file**, then fuzz targets, then helpers and fakes.
- `ui` has its own file map and naming rules in `ui/doc.go`: read it before
  adding a file there.

### Comments

- **One line by default, two when load-bearing, three only for a package
  doc or an invariant the design rests on.** No comment line past 100 columns.
- **Say why, not what.** The code says what. A comment earns its place by
  naming the reason, the trap or the case that broke before.
- Plain sentences, as a person writes them: no em dashes, no semicolons, no
  headings or lists inside a comment.
- Doc comments start with the name (`// Restore rebuilds…`). A comment that
  stops being true is deleted or fixed in the same change, never left beside
  the new one.

### Errors

- **Wrap with `%w` and the package's name** (`fmt.Errorf("worktree: stage: %w", err)`),
  so a message says where it came from and `errors.Is` still works.
- **A condition callers branch on is a sentinel** (`worktree.ErrGone`,
  `routing.ErrNoSandbox`, `forget.ErrLive`), named `ErrX` and tested with
  `errors.Is`.
- **A bad tool call is a tool result, never a Go error**, so the model reads
  it and corrects itself. A failure the human should see is a `Notice`.
- An error that may carry a secret is redacted before it reaches the bus,
  since the bus feeds the store, the log and the model (`mcp.redact`).

### Concurrency and state

- **Typed atomics** (`atomic.Bool`, `atomic.Pointer[T]`) over `atomic.Value`,
  and a `sync.Mutex` beside the fields it guards, with a comment if that is
  not obvious.
- **Package-level state is rare and explained.** A `var` a test overrides
  (`retryWaits`, `questionSettle`, `coalesceWindow`) says so.
- **`ctx` is the first parameter.** A subscriber doing network or process
  work takes the session's ctx in `Watch` and derives every request from it.
  `logging` and `store` take none on purpose, to write the last records.
- A value read by another goroutine after an event is read *before* the
  event is published, never after (`Run` reads `leftOpen` before
  `SessionStarted`). The race detector catches the rest, and CI runs it.

### Current Go

- Use what the toolchain gives: `min` and `max`, `slices` and `maps`,
  `strings.SplitSeq` and `FieldsSeq`, `for range n`, `cmp.Or`, and in tests
  `t.Context()` and `b.Loop()`. `golangci-lint` (with modernize) flags most of
  what it would rewrite.
- Imports in three groups: the standard library, third-party, then this
  module. `goimports` keeps them that way.

### Interfaces

- **An interface found by type assertion gets a compile-time assertion**
  beside the implementation (`var _ Selector = gaugeWidget{}`), because a
  renamed method otherwise degrades silently instead of failing the build:
  `engine.Snapshotter` losing its name removes rollback entirely, and a
  widget losing `Validate` simply stops validating. One proved by an
  argument, a struct field or a return type needs no assertion and should
  not get one.
- An interface is declared where it is used, and kept to the methods that
  caller needs (`engine.Runner`, `engine.Worktreer`).

### Tests

- `testify` (`require` to stop, `assert` to carry on), table-driven where
  cases share a shape. Follow `internal/model/client_test.go`
  (`httptest`-backed), `internal/engine/engine_test.go` (a bus rig) and
  `ui/apply_test.go` (events in, state out).
- **Names say what must hold**: `TestRestore_KeepsACommittedCRLFFileCRLF`,
  not `TestRestore2`. A failing name should read as the broken promise.
- **A test for a bug fails without the fix.** Check it by putting the bug
  back (a mutation check) before trusting it, and never let a fuzzy or
  forgiving assertion pass the old behaviour too.
- Tests never touch the human's real state: `t.TempDir()`, a temp HOME, a
  `:memory:` store. Anything that must reach outside the process skips
  unless asked (`DETENT_LIVE`, `DETENT_STRESS`).
- Code that reads input nothing controls (command output, model text) gets
  a fuzz target, listed in CI's fuzz job.
