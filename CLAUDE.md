# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

detent is a TUI harness that pairs a small/local LLM (via any OpenAI-compatible
`/chat/completions` endpoint — LM Studio by default, OpenRouter or OpenAI also
work) with a human. The model proposes one shell command at a time toward a
stated goal; nothing ever runs without explicit human confirmation of the
literal command text. There is no closed capability registry — the model can
propose arbitrary `sh -c` commands, and the safety story rests entirely on
mandatory confirm, not on structural/typed restrictions.

A second, optional model — TypeSafe's Jev — classifies commands before and
after execution (mutability tier, scope risk, result status, render kind,
etc.) to drive confirm emphasis and output rendering. It's a pure classifier
with no control over what runs; without a `jev_api_key` configured, rows fall
back to heuristics and confirms stay neutral (see `internal/agentloop/risk.go`
`heuristicPost`/`FlagDanger`).

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
Run a single test: `go test ./internal/agentloop/ -run TestSession_RunGoal`

## Configuration

Precedence: flags > environment > config file > built-ins. Config file is
`./.detent.yaml` (repo-local, gitignored) or `~/.config/detent/config.yaml`;
see `detent.example.yaml` for every key. Relevant env vars: `DETENT_BASE_URL`,
`DETENT_MODEL`, `DETENT_API_KEY` (falls back to `OPENROUTER_API_KEY` then
`OPENAI_API_KEY`), `TYPESAFE_API_KEY` (enables the Jev judge). A `.env` in the
repo root is also loaded at startup (`cmd/detent/main.go` `loadDotenv`), real
env vars always win over it.

At startup `main.go` pings the proposer's `/models` endpoint and fails fast
with a clear message if it's unreachable — don't remove this, it's the
difference between a useful error and a raw dial failure on the first goal.

## Architecture

Package dependency flow (each layer only knows about the one below it):

```
cmd/detent  →  ui  →  agentloop  →  propose, shell, classify, usage
```

- **`internal/propose`** — the `Proposer` interface (`Propose(ctx, []Message) (Proposal, usage.Usage, error)`)
  plus `OpenAIProposer`, its reference implementation. `Message.Role` mirrors
  chat-completions roles (`user`/`assistant`/`tool`) deliberately: the whole
  design is one persistent, growing transcript, not disconnected per-goal
  requests — a new goal is just another `RoleUser` message appended to the
  same slice. `RoleTool` messages (command output fed back) are sent over the
  wire as role `"user"`, not `"tool"` — see the comment in `encoding.go`
  `toWireMessage`: hosted endpoints reject a bare `tool` role without a
  `tool_call_id`, and this keeps the adapter portable across any
  OpenAI-compatible backend. `Proposal.Command` is empty iff `Done` is true;
  `parseProposal` enforces that invariant when decoding the model's JSON.

- **`internal/shell`** — the only place `exec.Command` is called. `Run`
  executes via `sh -c` with a bounded timeout and per-stream output cap
  (`MaxOutputBytes`); a non-zero exit is a `Result`, not a Go `error`. `Stream`
  (in `stream.go`) is the live-output variant the TUI uses, emitting
  `StreamEvent`s as the command runs.

- **`internal/agentloop`** — the propose → confirm → execute → judge loop,
  one goal at a time, over an append-only `Session.Transcript`
  (`[]propose.Message` shared across every goal in the session). Two
  entry points:
  - `RunGoal` — blocking, used by the headless `-goal` CLI path; fails
    closed if `Confirm` is nil.
  - `BeginGoal`/`ProposeNext`/`Execute`/`JudgeResult`/`Record*` — the
    non-blocking primitives the TUI drives directly via the `Driver`
    interface (`internal/ui/model.go`), because Bubble Tea's async,
    message-driven `Update` loop can't sit inside a blocking callback.

  `risk.go` holds both judgment paths: `judgePre` (mutability + scope risk,
  before confirm) and `judgePost` (result status + render kind + attention +
  goal-achieved, after execution), each with a heuristic fallback when no
  Judge is wired. `FlagDanger` is a regex safety net that only ever adds
  confirm emphasis (`Dangerous`/`RiskNote`) — it must never suppress or
  soften a confirm.

- **`internal/classify`** — the `Judge` interface (`Ask(ctx, State,
  Questions) (Answers, Usage, error)`) and `JevJudge`, the HTTP adapter for
  TypeSafe's Jev. Swappable the same way `propose.Proposer` is: a different
  classifier is a new file implementing `Judge`, nothing else changes.
  `ChoiceQuestion.Criteria` is `map[string]any` (structured `{what, not_for}`
  objects work better than flattened strings — validated in early spikes) and
  `ScoreQuestion.Levels` is an ordered `[]string` (index = level), matching
  Jev's real wire API.

- **`internal/ui`** — the Bubble Tea TUI. `model.go` defines `Driver`, the
  narrow surface the UI needs from `*agentloop.Session` (kept as an interface
  so UI tests can fake it without a live model endpoint). `flow.go` sequences
  propose → confirm → execute → judge as `tea.Cmd`s; `keys.go` and
  `history.go` handle navigation between goal/command blocks; `view.go`
  renders. Sub-packages `slash` (slash-command parsing/autocomplete),
  `status` (usage/timing formatting), `tabular` (table rendering for
  ps/df/ls-shaped output), `markdown` (glamour wrapper), `theme`, and
  `island` are each self-contained rendering/parsing helpers with their own
  tests.

- **`internal/usage`** — timing and token accounting (`Tracker` → `Goal` →
  `Step`), independent of everything else; `agentloop` and `ui` both just
  attach measurements to it.

- **`internal/config`** — `Load` (file) and `Resolve` (layers flags > env >
  file > built-ins, field by field via `Config.apply`). Both `propose` and
  `config` independently declare the same LM Studio defaults
  (`http://localhost:1234/v1`, `prism-ml/bonsai-27b`) — that duplication is
  intentional so `propose` has no dependency on `config`.

## Conventions

- Tests use `testify` (`require`/`assert`) with table-driven cases — follow
  the existing pattern in `internal/propose/openai_test.go` (`httptest`-backed)
  and `internal/agentloop/*_test.go` for new tests in those packages.
- Commit messages: short and concise, no body, no references to plan
  documents or section numbers.
- `docs/` is gitignored — planning documents live there but are never
  committed to the repo.
- Every command the model proposes requires an explicit human `y`/`n`
  confirm, with no exceptions and no auto-run path. Any change that could
  weaken this (auto-approval, skipping confirm for a "safe" command class,
  etc.) is out of scope by design — see the `ConfirmFunc` doc comment in
  `internal/agentloop/loop.go`.
