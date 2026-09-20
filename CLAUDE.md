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

## Configuration

Precedence: flags > environment > config file > built-ins. Config file is
`./.detent.yaml` (repo-local, gitignored) or `~/.config/detent/config.yaml`;
see `detent.example.yaml` for every key. Relevant env vars: `DETENT_BASE_URL`,
`DETENT_MODEL`, `DETENT_API_KEY` (falls back to `OPENROUTER_API_KEY` then
`OPENAI_API_KEY`), `TYPESAFE_API_KEY` (enables the Jev judge), `DETENT_THEME`
(one of `internal/ui/theme.Themes`' names: `dark`, `light`, `solarized`,
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
cmd/detent  →  ui, resolver, agent, classify, config, propose (Ping only)
resolver    →  ui (Driver + DTOs), agent, propose, host, usage, fileio
ui          →  its own subpackages only (editor, slash, status, tabular,
                markdown, theme, island, tree, layout)
agent       →  propose, host, classify, usage
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

- **`internal/host`** — the only place `exec.Command` is called, all in
  one file (`shell.go`). `Shell.Run` executes via `sh -c` with a bounded
  timeout and per-stream output cap (`MaxOutputBytes`); a non-zero exit
  is a `Result`, not a Go `error`. It also takes a `chan<- StreamEvent`
  for live output (nil is fine — Run just skips sending) and closes it
  once output ends, instead of taking a callback. `Shell{}` is the
  unsandboxed default `agent.Runner`.

- **`internal/agent`** — the propose → confirm → execute → judge loop,
  one goal at a time, over an append-only `Session.Transcript`
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
  holds the `Session` struct and result/record
  types; `loop.go` holds the two entry points:
  - `RunGoal` — blocking, used by the headless `-goal` CLI path; fails
    closed if `Confirm` is nil.
  - `BeginGoal`/`ProposeNext`/`RecordStep`/`Execute`/`JudgeResult`/
    `Record*` — the non-blocking primitives `resolver` drives on the
    TUI's behalf, because Bubble Tea's async, message-driven `Update`
    loop can't sit inside a blocking callback. `RecordStep` is the one
    place `AddStep`/`SetPropose`/`SetJudgePre`/`SetDwell` happen,
    shared by both `RunGoal` and the non-blocking path so the sequence
    isn't hand-rolled twice.

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

- **`internal/ui`** — the Bubble Tea TUI, decoupled from the core harness
  entirely: it imports nothing under `internal/*` except its own
  subpackages. `driver.go` declares `Driver` (the narrow surface the UI
  needs) plus every DTO its methods use (`Proposal`, `PreJudgment`,
  `PostJudgment`, `ExecutedCommand`, `GoalResult`, `Result`, `Usage`,
  `GoalStats`/`StepStats`, `RenderKind`/`EndReason` and their constants) —
  flat mirrors of `agent`'s/`usage`'s/`propose`'s/`host`'s own types,
  owned by `ui` so a change to any of those doesn't ripple into `ui`
  directly; `internal/resolver` is the one thing that imports both sides
  to translate between them. `model.go` holds `Model`, `stepRow`/
  `goalBlock`, and the top-level `Update` dispatcher. `goal_flow.go`
  sequences propose → confirm → execute → judge as `tea.Cmd`s (`approve`/
  `decline` call `Driver.RecordStep` once rather than touching usage
  bookkeeping themselves — `ui` has no way to reach `usage.Step`'s
  mutators at all now); `tool_flow.go`/`exec_flow.go`/`save_flow.go`
  handle the slash-command, streaming, and file-save flows the same way;
  `keys.go` routes keystrokes. `render_view.go`, `history_view.go`,
  `confirm_view.go`, `usage_view.go`, and `view.go` render — anything
  producing display strings from `Model` state lives in a `_view.go`
  file. Sub-packages `slash` (slash-command parsing/autocomplete),
  `status` (usage/timing formatting — switches on the same Status*/
  RenderKind string values `ui.PostJudgment` carries, duplicated as
  literals rather than importing anything to get them), `tabular` (table
  rendering for ps/df/ls-shaped output), `markdown` (glamour wrapper),
  `theme`, `tree`, `layout`, and `island` are each self-contained
  rendering/parsing helpers with their own tests; `editor` wraps a
  `textarea` around content the caller already read (it doesn't read
  files itself, only diffs in-memory content via `go-udiff` directly).

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

`ui`'s DTOs (`internal/ui/driver.go`) are hand-mirrored from `agent`'s/
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
  carries, a new dialog) — `internal/ui` only.
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
