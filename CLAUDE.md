# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

detent is a TUI harness that pairs a small/local LLM (via any OpenAI-compatible
`/chat/completions` endpoint — LM Studio by default, OpenRouter or OpenAI also
work) with a human. The model proposes one shell command at a time toward a
stated goal. There is no closed capability registry — the model can propose
arbitrary `sh -c` commands, and the safety story rests entirely on the
Dangerous flag, not on structural/typed restrictions.

A command flagged Dangerous is shown to a human, who reads the literal text
and must explicitly approve it; every other command runs straight through
with no confirm at all. Dangerous comes from a second, optional model —
TypeSafe's Jev — classifying mutability and scope risk before execution
(also driving result-status/render-kind classification after execution), OR'd
with `FlagDanger`, a regex backstop (`internal/agent/risk.go`). Without a
`jev_api_key` configured, only the regex backstop applies and rows fall back
to heuristics for post-execution rendering. Widening what counts as Dangerous
(e.g. also confirming `writes_workspace`-tier commands, not just
`system_affecting`/`likely_irreversible`) is a one-line change in the
`pre.Dangerous` check in both `internal/agent/loop.go` and
`ui/goal_flow.go`'s `onPropose`.

## Commands

```sh
make build         # go build -o bin/detent ./cmd/detent
make run           # launch the TUI (go run, no build step)
make run-headless GOAL="..."   # run one goal headlessly and exit
make test          # go test ./...
make vet           # go vet ./...
make fmt           # gofmt -w .
make fmt-check     # fail if anything isn't gofmt'd
```

Run a single package's tests: `go test ./internal/propose/...`
Run a single test: `go test ./internal/agent/ -run TestSession_RunGoal`

CI (`.github/workflows/ci.yaml`) runs the same three things on every push
and PR: `go test -race -cover` on ubuntu and macos, gofmt/vet/`go mod
tidy`, and a five-target cross-build with `CGO_ENABLED=0`. Windows is
cross-built but never tested, since every command goes through `sh -c`
and the sandbox talks to containerd over a unix socket. The containerd
tests skip themselves in CI; rollback is covered locally against colima.
Keeping `CGO_ENABLED=0` green is what makes a pure-Go SQLite driver the
only option when persistence lands.

## Configuration

Precedence: flags > environment > config file > built-ins. Config file is
`./.detent.yaml` (repo-local, gitignored) or `~/.config/detent/config.yaml`;
see `detent.example.yaml` for every key. Relevant env vars: `DETENT_BASE_URL`,
`DETENT_MODEL`, `DETENT_API_KEY` (falls back to `OPENROUTER_API_KEY` then
`OPENAI_API_KEY`), `TYPESAFE_API_KEY` (enables the Jev judge), `DETENT_CONTEXT_TOKENS`
(transcript budget; unset or unparseable falls through to the next
layer rather than zeroing it), `DETENT_THEME`
(one of `ui/theme.Themes`' names: `dark`, `light`, `solarized`,
`dracula`). A `.env` in the repo root is also loaded at startup
(`cmd/detent/main.go` `loadDotenv`), real env vars always win over it.

At startup `main.go` pings the proposer's `/models` endpoint and fails fast
with a clear message if it's unreachable — don't remove this, it's the
difference between a useful error and a raw dial failure on the first goal.
It also applies the theme before building the TUI: `theme.Apply` sets the
active colors, `ui.RefreshStyles` rebuilds every style already baked from
the old ones.

## Architecture

Package dependency flow — `ui` and `agent` never import each other;
`resolver` is the only package that imports both:

```
cmd/detent  →  ui, resolver, agent, classify, config, routing, propose (Ping only)
resolver    →  ui (Driver + DTOs), agent, propose, host, usage, fileio, viewgen
ui          →  its own subpackages (editor, welcome, render, status,
                markdown, theme, island, tree, layout) + viewspec
viewspec    →  the standard library, nothing else
logging     →  the standard library, nothing else
agent       →  propose, host, classify, usage, viewgen (render kinds)
config      →  agent, classify, propose, sandbox (for their defaults only)
host        →  capture
sandbox     →  capture (never host or agent)
routing     →  agent, sandbox
```

- **`internal/propose`** — `OpenAIProposer`, the reference implementation of
  `agent.Proposer` (`Propose(ctx, []Message) (Proposal, usage.Usage,
  error)` — the interface lives in `agent`, its one consumer, not here;
  `propose` exports only the data types and the implementation). `Message.Role` mirrors
  chat-completions roles (`user`/`assistant`/`tool`) deliberately: the whole
  design is one persistent, growing transcript, not disconnected per-goal
  requests — a new goal is just another `RoleUser` message appended to the
  same slice. `RoleTool` messages (command output fed back) are sent over the
  wire as role `"user"`, not `"tool"` — see the comment in `encoding.go`
  `toWireMessage`: hosted endpoints reject a bare `tool` role without a
  `tool_call_id`, and this keeps the adapter portable across any
  OpenAI-compatible backend. `Proposal.Command` is empty iff `Done` is true;
  `parseProposal` enforces that invariant when decoding the model's JSON.
  `prompt.go`'s `Environment` is what the system prompt says about
  where commands actually run — OS/arch, working directory, sandboxed,
  network, undoable. `cmd/detent` fills it from the wiring it just
  did; the zero value falls back to `LocalEnvironment()`, this
  process's own machine. That fallback is only right unsandboxed:
  describing detent's own macOS while commands run in an Ubuntu
  container is how BSD flags end up in a Linux container.

- **`internal/capture`** — the bounded-output-capture primitives shared
  by every command backend: `Result`, `StreamEvent`, `MaxOutputBytes`,
  and `ScanCapped` (reads a stream line by line, capping it and
  emitting a `StreamEvent` per line). Has no `exec.Cmd`/containerd
  knowledge of its own — `host` feeds it `os/exec` pipes, `sandbox`
  feeds it a polling file reader (see below). `host.Result`/
  `host.StreamEvent`/`host.MaxOutputBytes` are aliases onto this
  package's own (`internal/host/types.go`), so every existing
  `host.Result` reference elsewhere is unaffected by the split.

- **`internal/host`** — the only place `exec.Command` is called, all in
  one file (`shell.go`). `Shell.Run` executes via `sh -c` with a bounded
  timeout and per-stream output cap (`MaxOutputBytes`); a non-zero exit
  is a `Result`, not a Go `error`. It also takes a `chan<- StreamEvent`
  for live output (nil is fine — Run just skips sending) and closes it
  once output ends, instead of taking a callback. `Shell{}` is the
  unsandboxed default `agent.Runner`.

- **`internal/sandbox`** — `Container`, a session-scoped
  containerd-backed `agent.Runner` (one persistent container per
  session, not per command, so filesystem state accumulates across
  commands and can be checkpointed). Imports `capture`, never `host`
  or `agent` — `Run`'s signature is already spelled in `capture`'s
  types, which is what lets it satisfy `agent.Runner` structurally.
  Output is captured by shell-redirecting into files inside the
  workspace bind mount and polling them (`tail.go`), not via
  containerd's own `cio` FIFO streaming: a FIFO needs the shim and the
  reader on the same kernel, which doesn't hold once the daemon runs
  inside a VM (colima, on macOS). `Snapshot`/`Rollback` (`snapshot.go`)
  map onto containerd's `Prepare`/`Commit` vocabulary and use plain
  `string` checkpoint IDs for the same reason; `routing.WrapSandbox`
  adapts that string to `agent.SnapshotID` when wiring
  `agent.Snapshotter`. **A rollback does not revert the workspace** —
  that's a bind mount to the user's real directory, deliberately not
  part of the snapshot, so only container state outside it is
  restored (`TestContainer_RollbackLeavesTheWorkspaceAlone` pins
  this). Checkpoints are held by a per-session containerd **lease**
  (`snapshot.go`'s `pin`): the GC keeps a snapshot only while a
  container or lease references it, so without one a rollback orphans
  every later checkpoint and the next GC pass sweeps them — rollback
  then works exactly once
  (`TestContainer_CheckpointsSurviveGarbageCollection`). Networking is
  a posture, `sandbox.NetworkHost` (the default) or `NetworkNone`:
  host drops the network namespace so the container inherits the
  containerd daemon's, which on macOS is the colima VM's and on Linux
  is the machine's. `DefaultImage` still carries git/curl so goals
  work under `NetworkNone` too. Real containerd daemon required
  for its own tests (`container_test.go`), skipped when unreachable.

- **`internal/worktree`** — checkpoints the human's own working
  directory, which the container snapshot never covers: `/workspace`
  is a bind mount from outside it, and that's where a goal does its
  real work. Git plumbing against a scratch `GIT_INDEX_FILE`, so
  `add -A`/`write-tree` capture tracked *and* untracked files without
  touching the user's index, branch or stash — and `.gitignore` is
  honoured for free, which is both correct (build output isn't state)
  and what keeps a per-step capture cheap. `agent.Session` records one
  per sandboxed step (`ExecutedCommand.Worktree`), `PlanRollback`
  reports what a restore would change, and `Rollback`'s `revertFiles`
  decides whether it happens — the UI asks first, because the
  workspace can hold edits detent never made. `markUnseen` flags paths
  that changed after the last checkpoint: nothing detent ran accounts
  for those, so reverting them destroys work it never made, and the
  confirm says so in as many words. Its default answer is the
  non-destructive one, and the file list takes the output pane rather
  than the modal — a wide-reaching goal touches more paths than a box
  can hold, and a list you can't read to the end isn't one you can
  approve.

- **`internal/routing`** — `Selector`, the `agent.RunnerSelector`
  `cmd/detent` wires: host vs. sandbox per command, deliberately dumb
  for v1 (a global toggle; `PreJudgment` is threaded through but
  unused, ready for a Jev-informed rule later). `WrapSandbox` is the
  other half — see above. Imports `agent` and `sandbox`; neither
  imports it back, so it's the one place allowed to bridge them.

- **`internal/agent`** — the propose → confirm → execute → judge loop,
  one goal at a time, over a single `Session.Transcript`
  (`[]propose.Message` shared across every goal in the session). Has no
  idea `ui` or `resolver` exist. `interfaces.go` declares every interface
  `agent` consumes: `Proposer`, `Judge`, `Confirmer`, `Runner` — the last
  taking a `chan<- host.StreamEvent` directly (may be nil) rather than a
  callback. Unlike `Judge` (nil just disables judging), `Proposer`/`Run`
  have no auto-default: `BeginGoal` fails loud if either is nil, the same
  way `Confirm` fails the whole session closed. `host.Shell` runs
  commands directly on the host, unsandboxed, and is wired explicitly by
  `cmd/detent/main.go` — a future sandboxed `Runner` is a wiring choice
  there, not a hidden fallback inside `agent`. Declared at the consumer,
  not the producer — `propose` and `classify` ship only data types and
  implementations. `probe` declares its own identically-shaped `Judge`,
  since it sits below `agent` and can't import it back. `options.go`
  holds `Option`/`With*`/`New`, same split as `propose`. `session.go`
  holds the `Session` struct and result/record types. `loop.go` holds
  `RunGoal` (blocking, the headless `-goal` path, fails closed without
  `Confirm`) plus the non-blocking primitives `resolver` drives on the
  TUI's behalf instead — `BeginGoal`/`ProposeNext`/`RecordStep`/
  `Execute`/`JudgeResult` — since Bubble Tea's async `Update` loop
  can't sit inside a blocking callback. `record.go` holds the
  `Record*` family that closes a goal (or notes a standalone action).
  `snapshot.go` holds `Snapshot`/`Rollback`: `Rollback(res, N)` undoes
  step N of that goal **and everything after it**, restoring the checkpoint from
  before N ran — the one after N-1, or `GoalResult.Baseline` (captured
  in `BeginGoal`) when N is the first step. Naming a step the user can
  see and having it disappear is the point; restoring *to* a step
  instead would make `/rollback <last>` a no-op.

  `compact.go` keeps the transcript under `Session.ContextTokens`
  (`DefaultContextTokens` when unset, `context_tokens` in config),
  since every propose call resends all of it. The budget is stated in
  tokens because that's the unit the status bar and `/usage` already
  show; `BytesPerToken` converts it to what `transcriptBytes` can
  actually count, and is a ceiling to stay under rather than an
  accounting of what the endpoint bills. It runs in `BeginGoal` only —
  a goal boundary is the one place the model doesn't need the turns
  being dropped, and it happens *before* the new goal is appended so
  the fresh ask can't be what goes. Because compaction rewrites the
  front of the slice, `TranscriptMark`/`BaselineMark` count **appends**
  (`Session.seq`), not slice positions; `Session.index` converts one
  back, and `Rollback` errors rather than restoring a container to a
  state the transcript can no longer describe. The invariant to hold
  onto: `seq - dropped == len(Transcript)`. A nil `Summarizer` is fine
  — the dropped turns become a note saying they're gone.

  `judge.go` holds both judgment paths:
  `judgePre` (mutability + scope risk, before confirm) and `judgePost`
  (result status + render kind + attention + goal-achieved, after
  execution), each with a heuristic fallback when no Judge is wired, plus
  the `NewPreJudgment`/`NewPostJudgment` constructors that centralize
  their `-1` ("unknown") sentinel defaults. `risk.go`'s `FlagDanger` is a
  regex safety net that only ever adds confirm emphasis
  (`Dangerous`/`RiskNote`) — it must never suppress or soften a confirm.

- **`internal/classify`** — `jev.go` holds `JevJudge`, the HTTP adapter
  for TypeSafe's Jev; `options.go` holds its `Option`/`With*`/
  `NewJevJudge`, same split as `propose`/`agent`. `judge.go` holds the
  question/answer vocabulary types (`State`, `Questions`, `Answers`,
  `Usage`) every `Judge` implementation speaks. A different classifier is
  a new type implementing `agent.Judge`'s `Ask` method — nothing here
  needs to change, since classify doesn't own that interface.
  `ChoiceQuestion.Criteria` is `map[string]any` (structured `{what, not_for}`
  objects work better than flattened strings — validated in early spikes) and
  `ScoreQuestion.Levels` is an ordered `[]string` (index = level), matching
  Jev's real wire API.

- **`internal/resolver`** — the translation layer between `ui`'s
  vocabulary and the core harness's: `Resolver` wraps `*agent.Session` and
  implements `ui.Driver`, translating every value each way in `convert.go`
  (`stream.go`'s `relayEvents` does the same for `StreamEvent`, via one
  goroutine per `Execute` call that ranges agent's channel and forwards
  translated events onto `ui`'s, dropping under backpressure rather than
  blocking the running command). It's the
  only package importing both `ui` and `agent` — neither of them may
  import it back. `cmd/detent` wires `resolver.New(sess)` into `ui.New`;
  the headless `-goal` path talks to `*agent.Session` directly and never
  touches `resolver`/`ui` at all. `ReadFile`/`SaveFile` also wrap
  `internal/fileio`'s `Read`/`Write` here, so `ui` never imports `fileio`
  either. `GoalResult`/`StepHandle`'s `Ref any` field is how a `ui.Driver`
  caller's handle round-trips back to the live `*agent.GoalResult`/
  `*usage.Step` resolver needs on the next call — an opaque token `ui`
  only ever threads through, never inspects.

- **`ui`** — the Bubble Tea TUI, decoupled from the core harness
  entirely: it imports nothing under `internal/*` — only its own
  subpackages and `viewspec`, which is likewise outside `internal/`
  and depends on nothing. That independence is why it sits outside `internal/`
  and is importable on its own: everything it needs arrives through
  `Driver` and `SessionInfo`, so it never reaches into the harness.
  `driver.go` declares `Driver` (the narrow surface the UI
  needs) plus every DTO its methods use (`Proposal`, `PreJudgment`,
  `PostJudgment`, `ExecutedCommand`, `GoalResult`, `Result`, `Usage`,
  `GoalStats`/`StepStats`, `RenderKind`/`EndReason` and their constants) —
  flat mirrors of `agent`'s/`usage`'s/`propose`'s/`host`'s own types,
  owned by `ui` so a change to any of those doesn't ripple into `ui`
  directly; `internal/resolver` is the one thing that imports both sides
  to translate between them. `model.go` holds `Model`, `stepRow`/
  `goalBlock`, and the top-level `Update` dispatcher — which routes the
  message and then re-syncs the panes **once**, so no handler has to
  remember to refresh anything. What history shows is derived per
  render by `historyWindow`; the only scroll state kept is its offset.
  `prompt.go` owns the input box and its slash dropdown together, so
  nothing else reaches into the textarea or the match list.
  `slash.go` is the slash-command registry, and each entry carries its
  own handler so a command can't be listed without working or work
  without being listed; the dropdown scrolls once the registry
  outgrows `maxSlashRows`, its window derived from the cursor rather
  than stored beside it. The input keeps focus while a goal runs
  (`owner()` returns `ownerBusy`), which is what makes `/abort`
  typeable mid-run — blurring it sent every key down the history
  branch and `busyKey` was unreachable. `welcome_view.go` is derived state, not a mode:
  it shows whenever no row is focused, which is why `/new` only has to
  drop the blocks to bring it back.
  `goal_flow.go`
  sequences propose → confirm → execute → judge as `tea.Cmd`s (`approve`/
  `decline` call `Driver.RecordStep` once rather than touching usage
  bookkeeping themselves — `ui` has no way to reach `usage.Step`'s
  mutators at all now); `tool_flow.go`/`exec_flow.go`/`save_flow.go`
  handle the slash-command, streaming, and file-save flows the same way;
  `keys.go` decides what a keystroke means and `nav.go` what it does.
  `esc` and `tab` are intercepted in `handleKey` before `owner()`
  dispatches, so anything wanting either has to be handled there —
  `onEscape` backs out of the innermost thing first (dropdown, then a
  running goal, then the output pane). Quitting is `ctrl+c` or
  `/quit` only; no bare letter ends a session.
  Anything producing display strings from `Model` state lives in a
  `_view.go` file: `view.go` composes the screen, `layout_view.go`
  does the sizing maths, `chrome_view.go` the bars and pane headers,
  `detail_view.go` the output pane, plus `history_view.go`,
  `confirm_view.go` and `usage_view.go`. The `#N` marker on a step
  counts across the **session**, not within its goal, and `/rollback N`
  resolves that number back to the goal that owns it
  (`findStep`/`truncateFrom`): numbering per goal made every row read
  "#1" and the argument silently addressed the last goal instead of
  the step being pointed at. History draws each goal as a
  block against a coloured rail (`railed`/`railStyle`) rather than
  separating them with a divider: a rail marks how far a block
  reaches, not just where two meet, and its colour carries the
  outcome. Every width a block renders at comes off `blockWidth`,
  which subtracts the rail gutter —
  `TestHistory_RowsFitThePane` is what stops one overflowing.

  What can leave the package is what takes values rather than a
  `Model` — Go keeps a method in its receiver's package, so the ~80
  `func (m Model)` ones can only move by first becoming their own
  type, the way `prompt` did. Sub-packages: `welcome` (the boot pane,
  handed a `welcome.Facts` so it reads nothing of the harness),
  `render` (diff colouring for the save confirm; the output pane's own
  transforms live in `viewspec`), `status` (usage/timing formatting — switches on the same
  Status*/RenderKind string values `ui.PostJudgment` carries,
  duplicated as literals rather than importing anything to get them),
  `markdown` (glamour wrapper),
  `layout` (`Split` and `Truncate`), `theme`, `tree`, and `island`.
  Each owns its own styles off `theme` and is rebuilt by
  `ui.RefreshStyles`; `editor` wraps a `textarea` around content the
  caller already read (it doesn't read files itself, only diffs
  in-memory content via `go-udiff` directly).

- **`viewspec`** — the view interpreter, outside `internal/` like `ui`
  and stricter: it imports **only the standard library**, enforced by
  `TestPackage_DependsOnStdlibOnly`. A `Spec` says how to read a
  command's output (`Parse`, a named-capture regexp or column map) and
  how to draw what was read (`Blocks`, a flat list over a closed widget
  vocabulary). Eight parse kinds — `lines`, `columns`, `fixed` (slices
  at the header's own offsets, for multi-word headings like
  `CONTAINER ID`), `delimited`, `pairs`, `indent` (leading whitespace
  becomes a depth), `json`, `none` — and fourteen widgets: `text`,
  `table`, `list`, `keyvalue`, `tree`, `meter`, `bar`, `sparkline`,
  `badges`, `log`, `errors`, `json`, `diff`, `code`, plus `row`.
  A `row` lays its `Panes` side by side, nesting capped at one level so
  the schema stays finite — a recursive `$ref` is where strict mode's
  backend portability gets thin. It is the one kind the **interpreter**
  draws rather than a widget: `Widget.Draw` is handed a Block and Data,
  never the registry, so it could not resolve its children's widgets.
  The registry extends leaves; layout is geometry and belongs to the
  interpreter. Three calls priced by frequency: `Compile` once per spec,
  `Bind` once per output, `Draw` per frame — `Painter` is on `Frame`,
  not `Compiled`, so the first two are pure data and test with no
  styling at all. It declares `Painter`, `Widget` and `Extractor`
  because it calls them; `ui/painter.go` and `ui/views.go` implement and
  register them. `Registry.Schema()` describes the registered vocabulary
  as JSON Schema, so generation can't drift from what will actually
  draw; each widget's own `Describe` carries structured
  `{what, not_for, examples}` criteria into a `widget_guide`, for the
  calibration reason `internal/agent/judge.go` states at nine options. Any unresolved binding fails the **whole** view (`BindError`)
  and the caller falls down its spec chain — a table with one silently
  empty column is worse than plain text. It runs nothing:
  `Bound.Action` returns an `on_enter` template with `{field}`
  substituted, and `ui`'s `enter` seeds the prompt with it. A view
  carries its provenance (`shipped`/`saved`/`generated`) across the
  Driver in `ui.GeneratedView`. `viewportHeader` names only what a
  model had a hand in (`saved`, `generated`) plus `generate declined`,
  which is otherwise indistinguishable from never having tried;
  `built-in` and `shipped` are detent's own work and draw without
  comment, since naming them on every row was noise. The pane always
  draws from a spec, so there is no "off": `views: saved` draws only
  from specs that already exist, `views: generate` also authors one
  when nothing covers the output. Only `generate` ever writes a spec,
  which is why `saved` alone never grows the set. `Generate` asks the
  model before falling back to a shipped spec, so shipping one is a
  floor rather than a ceiling.

  **There is one render path.** `ui/views.go`'s `specChain` tries, in
  order: a spec keyed to the command (`normaliseCommand`), the built-in
  `fallbackSpecs` entry for whatever `render_kind` judged the output to
  be, then plain bytes. So `render_kind` no longer renders anything — it
  *chooses a prebuilt spec*, which is why `styledBody`'s switch and
  `focusedTable` are gone. `Draw` returns a `Render` carrying
  `CursorLine`, which `refreshViewport` uses to scroll the viewport to
  the selection, so generated views scroll like any other output.
  `ui/tabular` was `focusedTable`'s engine and is deleted; a richer
  table is a widget registered over the built-in, not a second path.

- **`logging`** — the structured log, outside `internal/` like `ui` and
  `viewspec` so anything may import it; stdlib only (`log/slog`), so no
  package's import rules break. **One JSONL stream per session**
  (`~/.local/state/detent/logs/<session>.jsonl`), never one file per
  component: the unit anyone investigates is a *step*, and a step
  crosses four or five components, so splitting by component would make
  filtering easy and correlating impossible. A `component` field gives
  the split for free and keeps the join. `events.go` is the closed
  vocabulary: an `event` name is a record's primary key, since a query
  cannot match free text reliably. Correlation rides the context
  (`WithGoal`/`WithStep`), so a mark set once upstream reaches every
  record beneath it and `step` is the same number the UI shows.
  `Body` withholds prompts, replies and output unless `log_bodies` is
  set, because they carry secrets and bulk. `viewspec` deliberately
  does not log: it returns typed errors and the caller records them,
  which is what keeps it embeddable.

- **`internal/usage`** — timing and token accounting (`Tracker` → `Goal` →
  `Step`), independent of everything else; `agent` attaches measurements
  to it directly, `resolver` translates it into `ui`'s own `GoalStats`/
  `StepStats`/`Snapshot` for the `/usage` overlay and status bar — `ui`
  never sees a `usage.*` type.

- **`internal/config`** — `Load` (file) and `Resolve` (layers flags > env >
  file > built-ins, field by field via `Config.apply`). Both `propose` and
  `config` independently declare the same LM Studio defaults
  (`http://localhost:1234/v1`, `prism-ml/bonsai-27b`) — that duplication is
  intentional so `propose` has no dependency on `config`.

### Keeping `ui` and `agent` in sync

`ui`'s DTOs (`ui/driver.go`) are hand-mirrored from `agent`'s/
`usage`'s/`propose`'s/`host`'s own types, not aliases of them — that's
the whole point of the split, but it means nothing forces a change on
one side to reach the other. Two different failure modes, two different
defenses:

- **Interface-shape drift** (a `Driver` method added, removed, or its
  signature changed) — the compiler catches this for you. `resolver.go`
  has `var _ ui.Driver = (*Resolver)(nil)`, so `Resolver` fails to build
  until every method exists with the right signature; `ui/testutil_test.go`'s
  `fakeDriver` has to satisfy the same interface, so `ui`'s own tests
  won't build either until its fake is updated too. No discipline
  required here — just fix the compile errors in the order they appear.
- **Field-level drift** (`agent` grows a field nothing forces you to
  surface) — this one compiles fine either way, so it's on you. The
  rule: whenever a change touches a type in `agent`/`usage`/`propose`/
  `host` that has a mirror in `ui/driver.go`, go decide on purpose in
  `internal/resolver/convert.go` whether the new data should cross the
  boundary — don't let it be discovered later as "why isn't X showing in
  the UI." `internal/resolver/driver_test.go` drives a real
  `*agent.Session` through a real `*Resolver` and asserts on the DTOs
  that come out — run it right after any `agent` change, before touching
  `ui` at all, as the fastest signal the translation still holds.

### Adding a new feature

Start from what the feature actually is, and touch only the layers it
needs:

- **Pure UI** (a keybinding, a different rendering of data a DTO already
  carries, a new dialog) — `ui` only.
- **Pure core logic** (a new probe, a heuristic tweak, a proposer
  change) — `internal/agent` only, with `agent`'s own tests. It doesn't
  need to reach `ui` until something is meant to surface there.
- **Anything crossing the boundary** (new judgment data, a new Driver
  capability, a new terminal state) — follow the ripple top-down, one
  layer at a time, each with its own test before moving to the next:
  1. `agent` — add the field/method; prove it in `internal/agent`'s own tests.
  2. `resolver/convert.go` (and `driver.go` if it's a new method) —
     mirror the change; add/extend a case in `driver_test.go` proving it
     survives the round trip through a real `*agent.Session`.
  3. `ui/driver.go` — add the field to the matching DTO, or the new
     method to `Driver` (which forces `fakeDriver` to implement it too —
     the compiler won't let this step be skipped).
  4. `ui` — wire the real code to use the new data/method; add a test
     against `fakeDriver` in whichever `_test.go` file already covers
     that flow.

  Six small, mechanical edits beat one tangled one: a mistake in step 1
  shows up as an `agent` test failure, not a mysterious blank field
  three layers away in the TUI.

## Conventions

- Tests use `testify` (`require`/`assert`) with table-driven cases — follow
  the existing pattern in `internal/propose/openai_test.go` (`httptest`-backed)
  and `internal/agent/*_test.go` for new tests in those packages.
- Commit messages: short and concise, no body, no references to plan
  documents or section numbers.
- `docs/` is gitignored — planning documents live there but are never
  committed to the repo.
- Confirm is conditional on `PreJudgment.Dangerous`, not universal — see the
  `Confirmer` doc comment in `internal/agent/session.go`. A nil `Confirmer`
  still fails the whole session closed even for an all-safe goal, since
  relying on "it happens not to be called" isn't a substitute for wiring
  one at all.
