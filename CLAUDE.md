# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

detent is a TUI harness pairing a model with a human at the terminal.
You state a request; the agent calls tools until it has answered,
publishing everything it does on a bus the front-end subscribes to.

There is no closed capability registry. `bash` is one of the tools, so
the model can run anything, and the safety story rests entirely on the
Dangerous flag rather than on structural restriction. A Call flagged
Dangerous is shown to a human who reads the literal command and must
approve it; everything else runs straight through.

Dangerous comes from a **hook chain** (`internal/engine/hooks.go`),
cheapest first: each tool's declared mutability, a regex backstop, a
repeat check, then TypeSafe's Jev over the network. The chain folds
every answer with `event.Risk.Widen`, which takes a max and ORs
Dangerous, so **a hook can widen a verdict and never narrow it** —
arithmetic, not a convention. Widening what counts as Dangerous means
registering one more `Assessor`.

## Commands

```sh
make build         # go build -o bin/detent ./cmd/detent
make install       # build, then copy it to go env GOBIN (or GOPATH/bin)
make run           # launch the TUI (go run, no build step)
make test          # go test ./...
make vet           # go vet ./...
make fmt           # gofmt -w .
make fmt-check     # fail if anything isn't gofmt'd
```

Run one request headlessly: `./bin/detent -prompt "..."`, with
`-unattended` to decline every flagged Call instead of asking.

Run a single package's tests: `go test ./internal/engine/...`
Run a single test: `go test ./ui/ -run TestApply`

Two tests reach outside the process and skip by default:
`go test ./internal/model/ -run TestLive` with `DETENT_LIVE=1` checks
that the endpoint really does tool calling, and the containerd tests
skip themselves when no daemon answers.

CI (`.github/workflows/ci.yaml`) runs `go test -race -cover` on ubuntu
and macos, gofmt/vet/`go mod tidy`, and a five-target cross-build with
`CGO_ENABLED=0`. Windows is cross-built but never tested, since every
command goes through `sh -c` and the sandbox talks to containerd over
a unix socket. Keeping `CGO_ENABLED=0` green is what makes a pure-Go
SQLite driver the only option when persistence lands.

## Configuration

Precedence: flags > environment > config file > built-ins. Config file
is `./.detent.yaml` (repo-local, gitignored) or
`~/.config/detent/config.yaml`; see `detent.example.yaml` for every
key. Relevant env vars: `DETENT_BASE_URL`, `DETENT_MODEL`,
`DETENT_API_KEY` (falls back to `OPENROUTER_API_KEY` then
`OPENAI_API_KEY`), `TYPESAFE_API_KEY` (enables the Jev hook),
`DETENT_CONTEXT_TOKENS`, `DETENT_THEME` (one of `ui/theme.Themes`'
names). A `.env` in the repo root is also loaded at startup, and real
env vars always win over it.

At startup `main.go` pings the endpoint's `/models` and fails fast
with a clear message — don't remove it, it's the difference between a
useful error and a raw dial failure on the first request. It also
applies the theme before building the TUI: `theme.Apply` sets the
colors, `ui.RefreshStyles` rebuilds every style baked from the old
ones.

### The sandbox VM

`sandbox_mode: auto` needs colima running with a containerd socket at
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
**survives being disabled** — `--kubernetes=false` stops the service,
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

Everything is named for one of four scopes, and using the wrong word
is how a bug gets written:

| Term | Is | Unit of |
| ---- | -- | ------- |
| Session | process lifetime, one message log | the transcript |
| **Turn** | one human prompt and all the agent did about it | **undo**, the history block |
| **Step** | one model round trip | **the transcript's atom**, compaction |
| **Call** | one tool invocation | the row, approval, parallelism |

A Step holds zero or more Calls; a Turn holds Steps until the model
stops asking for tools. Steps are never shown — a human does not think
in model round trips.

### The transcript's atom is a Step

One assistant message carrying N `tool_calls` plus the N `tool`
messages answering them are **indivisible**. Endpoints reject a
`tool_calls` message whose answers are missing, and reject a `tool`
message answering nothing. Three mechanisms depend on it:

- **Abort** must still emit a result for every Call that never ran, or
  the failure surfaces on the *next* Step, far from its cause.
- **Compaction** moves whole Steps and never half of one.
- **`NoteContext`** lands between Steps, never inside one.

`internal/engine/transcript.go` is the only place that mutates the log,
which is what keeps this true.

### Undo is per Turn

One checkpoint, taken before the first Call runs, and the Turn is the
only rollback target. Not per Call: a forty-call Turn would mean forty
containerd snapshots, and "undo call #23" is not a thought anyone has.
Rolling back truncates the transcript to where that prompt landed,
which is a whole number of Steps by construction.

## Architecture

One bus carries everything. Facts are past tense and come from the
engine; intents are imperative and come from anyone. An extension
listens, publishes, or both — there is no second mechanism.

```
cmd/detent  →  ui, engine, model, tool, classify, config, routing, headless
ui          →  event, viewspec, views, version, logging + its own subpackages
engine      →  event, tool, model, capture, classify (via an interface)
tool        →  event
model       →  event
event       →  the standard library, plus viewspec
viewspec    →  the standard library, nothing else
views       →  viewspec
logging     →  the standard library
config      →  engine, model, classify, sandbox (for their defaults only)
host        →  capture
sandbox     →  capture (never host or engine)
routing     →  engine, sandbox
```

`ui` imports **nothing** under `internal/`. That used to need a
translation layer (`internal/resolver`) mirroring every type; now both
sides import `event` directly and the layer is gone. `event` earns
that by depending on almost nothing, and the rule is a property rather
than a list: **every non-stdlib package `event` imports must itself
import only the standard library.** `viewspec` and `google/uuid` both
qualify. `event/event_test.go` walks the imports and checks it, so
adding a fat dependency fails with the transitive import named.

- **`event`** — the shared vocabulary and the `Bus`. `Publish` never
  blocks, whoever is listening and however slowly, so publishing from
  inside a handler is safe and cannot deadlock. Each subscriber has
  its own queue: a lagging one grows it and drops only events that say
  they are `Lossy`, which is `OutputChunk` and nothing else. A dropped
  live line costs a redraw; a dropped `CallEnded` is a row that never
  finishes. `Record` carries a gapless `Ordinal`, so a subscriber
  that filters or drops can be told apart from one that lost
  something. Ids are `uuid.UUID` directly, with no
  wrapper type: `google/uuid`'s v7 is monotonic within a millisecond
  as well as across them, which matters because a Step mints all its
  Call ids inside one.

- **`internal/engine`** — the loop, and it drives itself. One
  goroutine, blocking and linear, reading intents and publishing
  facts; `Run` is the actor and `runTurn` reads top to bottom. That is
  what lets front-ends subscribe rather than call, and why there is
  one loop instead of a blocking one for headless and a shattered one
  for the TUI. `New` subscribes to intents, not `Run`, so a caller
  that publishes the moment it returns cannot lose the intent.
  `Abort` is handled in `dispatch` rather than queued to the Turn: a
  blocked Call never reaches a boundary, and the inbox is only drained
  at one. Read-only Calls run concurrently, capped; anything else runs
  serially in the order asked. A declined Call returns a result saying
  so and its siblings still run — **declining stops a Call, not a
  Turn.** `MaxSteps` defaults to 50 and is soft: hitting it publishes
  `BoundReached` and waits, because a human is watching and stopping
  dead is worse than asking.

- **`internal/tool`** — the closed set a model may call, each lowered
  to one shell command so the sandbox stays the only executor. A Tool
  is pure: `args → command`, which is why the whole layer tests with
  no I/O. `bash` is not privileged — same registry, same schema, same
  hook chain. If it ever needs a code path the others don't have, the
  registry is wrong. Every bad call comes back as a **tool result**,
  never a Go error: an unregistered name, arguments failing the
  schema, a hallucinated parameter. The model reads it and corrects
  itself, which is the entire point of a loop. Under `strict: true`
  every property must appear in `required`, so an optional parameter
  is nullable rather than omitted — found by running it, not by
  asserting on it.

- **`internal/model`** — the tool-calling client. `Complete` is one
  Step. A call whose `arguments` will not parse is **kept**, with
  `Err` set: the assistant message already named that id, so dropping
  it leaves the transcript owing an answer. `Environment` is what the
  prompt says about where commands run — describing this process while
  they run in a container is how BSD flags end up in a Linux one.

- **`internal/capture`** — the bounded-output primitives every backend
  shares: `Result`, `StreamEvent`, `MaxOutputBytes`, `ScanCapped`. No
  `exec.Cmd` or containerd knowledge of its own.

- **`internal/host`** — the only place `exec.Command` is called, all in
  `shell.go`. A non-zero exit is a `Result`, not an error.

- **`internal/sandbox`** — `Container`, a session-scoped containerd
  Runner, one per session so filesystem state accumulates. Imports
  `capture`, never `host` or `engine`, which is what lets it satisfy
  `engine.Runner` and `engine.Snapshotter` **structurally** — no
  adapter needed, since its checkpoints were already plain strings.
  Output is captured by shell-redirecting into files and polling them
  (`tail.go`), not containerd's FIFO streaming: a FIFO needs the shim
  and the reader on the same kernel, which stops holding once the
  daemon runs inside a VM. **`Run` is serialised by a mutex**: one
  task and one spec per container, so parallel Calls overwrote each
  other's and returned exit 0 with no output. Real parallelism needs
  one long-lived task and `task.Exec` per Call. **A rollback does not revert the
  workspace** — that is a bind mount to the user's real directory,
  deliberately outside the snapshot. Checkpoints are held by a
  per-session lease; without one the GC sweeps them and rollback works
  exactly once.

- **`internal/worktree`** — checkpoints the human's own directory,
  which the container snapshot never covers. Git plumbing against a
  scratch `GIT_INDEX_FILE`, so it captures tracked *and* untracked
  files without touching the index, branch or stash — and `.gitignore`
  is honoured for free.

- **`internal/classify`** — `JevJudge`, the HTTP adapter, and
  `RiskJudge`, which adapts it to the engine's hook chain. It answers;
  it never decides, because `Widen` folds its answer with everyone
  else's.

- **`internal/headless`** — one prompt on a terminal, no TUI. A bus
  subscriber like any front-end, which is what makes it a fair test of
  the engine's interface.

- **`internal/viewgen`** — writes a spec by asking the judge closed
  questions and assembling the answers, rather than asking a model to
  write JSON. That path existed, ran 31s median against 300ms, and
  could name a widget, a role or a field that did not exist. All three
  happened; none is representable from a list the program built.

- **`ui`** — the TUI, and nothing but a projection of the event
  stream. `apply.go` folds facts in and is the one place it learns
  anything; `intents.go` publishes and is the one place it asks for
  anything. Seven `tea.Cmd` constructors and eight message types
  collapsed to one of each, so a test drives it with a sequence of
  events and no harness at all. `ui/doc.go` is the file map and the
  naming rules; read it before adding a file.

  **A thing leaves `ui` when it stops needing Model.** That is why
  `island`, `layout`, `markdown`, `status`, `theme` and `welcome` are
  subpackages and nothing else is: they take values and return
  strings. The compiler enforces it, since a subpackage importing
  `ui` would be an import cycle. Rendering could go the same way once
  Model's state is passed to it as values — worth doing if a second
  front-end ever wants the same drawing, not for one.

- **`viewspec`** — the view interpreter, outside `internal/` and
  stricter than anything else: it imports **only the standard
  library**, enforced by `TestPackage_DependsOnStdlibOnly`. A `Spec`
  says how to read a command's output (`Parse`) and how to draw what
  was read (`Blocks`, over a closed widget vocabulary). Eight parse
  kinds and thirty widgets, each in its own `widget_*.go`. Numbers are
  read by `number`, not `strconv.ParseFloat`, which rejected every
  column `df` prints: `45%`, `1.2G` and `1,024` all came back 0 and
  drew an empty bar rather than an error anyone could see. Three calls
  priced by frequency: `Compile` once per spec, `Bind` once per
  output, `Draw` per frame — `Painter` is on `Frame`, not `Compiled`,
  so the first two are pure data and test with no styling at all.
  `Frame.Height` says how tall the pane is for widgets that can grow
  into it and **clips nothing**, because clipping is what would stop a
  long view scrolling. A widget must implement `Widget`; `Validator`,
  `Selector`, `Described` and `Container` are optional and found by
  type assertion. `Selector` is load-bearing: only a kind that can say
  which line the cursor is on may carry `on_enter`, because accepting
  it elsewhere drew a spec that looked right and did nothing when the
  human pressed enter.

- **`logging`** — the structured log, stdlib only so anything may
  import it. **One JSONL stream per session**, never one file per
  component: the unit anyone investigates is a step, and a step
  crosses four or five components, so splitting by component would
  make filtering easy and correlating impossible. `events.go` is the
  closed vocabulary; an event name is a record's primary key, since a
  query cannot match free text reliably. `Body` withholds prompts,
  replies and output unless `log_bodies` is set.

- **`views`** — every spec detent ships, keyed by command name
  (`ForCommand`) and by judged output shape (`ForKind`). Imports
  `viewspec` and nothing else, which is what lets both `ui` and
  `viewgen` read it without either importing the other.

- **`version`** — what this build calls itself, and nothing else.

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
a widget, which is right; the cost is that a widget without one is
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
  already carries) — `ui` only. Drive `apply` with events; no harness
  is needed.
- **Pure engine** (a new hook, a bound, a loop rule) —
  `internal/engine` only, with its own tests.
- **A new tool** — `internal/tool`: one file, a `Spec`, and a `Lower`.
  Nothing else changes.
- **Anything crossing the boundary** (new data a front-end must see) —
  add the field to the fact in `event`, publish it in `engine`, fold
  it in `ui/apply.go`. Three edits, each provable on its own, and the
  compiler catches the first two.

There is no DTO mirror to keep in step any more. The thing that
replaced it is the rule that `event` may import nothing but the
standard library and `viewspec` — break that and the boundary is back.

## Conventions

- Tests use `testify` (`require`/`assert`) with table-driven cases —
  follow `internal/model/client_test.go` (`httptest`-backed),
  `internal/engine/engine_test.go` (a bus rig) and `ui/apply_test.go`
  (events in, state out).
- An interface found by type assertion gets a compile-time assertion
  beside the implementation (`var _ Selector = gaugeWidget{}`), because
  a renamed method otherwise degrades silently instead of failing the
  build: `engine.Snapshotter` losing its name removes rollback entirely,
  and a widget losing `Validate` simply stops validating. One proved by
  an argument, a struct field or a return type needs no assertion and
  should not get one.
- Commit messages: short and concise, no body, no references to plan
  documents or section numbers.
- `docs/` is gitignored — planning documents live there but are never
  committed to the repo.
- Confirm is conditional on `event.Risk.Dangerous`, not universal. A
  Call nobody answers blocks its Turn rather than running, which is
  the right way round: the approval gate fails closed.
- Comments are one line by default, two when load-bearing, three only
  for a package doc or an invariant the design rests on.
