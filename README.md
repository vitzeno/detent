# detent

A terminal agent, calls tools until it has an answer,
asks before anything risky, and checkpoints each request so you can undo it

Needs an OpenAI-compatible `/chat/completions` endpoint with tool calling.
LM Studio, OpenRouter and OpenAI all work

```sh
make build && ./bin/detent            # sandboxed, needs containerd
make install                          # onto your PATH
./bin/detent -sandbox host            # no container
./bin/detent -prompt "find go files over 1MB"
```

## Turns, Steps and Calls

|      | Is                                       | Unit of                 |
| ---- | ---------------------------------------- | ----------------------- |
| Turn | your prompt and everything done about it | undo, one history block |
| Step | one model round trip                     | the transcript's atom   |
| Call | one tool invocation                      | one row, one approval   |

A Step can ask for several Calls and runs the read-only ones together. A Turn
runs Steps until the model stops asking for tools, capped at 50. The cap asks
whether to continue rather than stopping

Current Tools: `bash`, `read_file`, `write_file`, `list_dir`.

## Approving

A Call runs without asking unless it is flagged dangerous, you see the
literal command and answer it.

Flagging is a chain: the tool's own declared mutability, a regex backstop, a
repeat check, then TypeSafe's Jev if a key is set. Each link can raise the
verdict but none can lower it

Declining stops that Call, the agent reads the refusal andtries
something else

Typing while it works steers it

## Undoing

`/undo 2` restores the container to before your second request and trims the
transcript to match

Your working directory is mounted at `/workspace`, outside the snapshot

## Output

The output pane draws from a view spec: a parse for reading bytes into rows,
blocks for drawing them

Currently thirty widgets over eight parse kinds, including
gauges, histograms, box plots, gantt charts, braille scatter plots and
heatmaps

With `views: generate` and a Jev key it designs a spec for output nothing
covers

Jev answers closed questions rather than writing JSON, so it cannot
name a widget or field that does not exist

All takes about 600ms, then cached

## Sandboxing

```sh
brew install colima
colima start --cpu 6 --memory 8 --disk 30 --kubernetes=false
```

On Linux, run containerd and point `sandbox_socket` at it.

## Configuration

`./.detent.yaml` or `~/.config/detent/config.yaml`

All keys are documented in [`detent.example.yaml`](detent.example.yaml)

## Resuming

Every session is recorded to `~/.local/state/detent/events.db`, so it
can be replayed rather than reconstructed.

```sh
detent -sessions              # what can be resumed
detent -resume last           # continue the most recent
detent -resume <id>           # or a specific one
```

`/sessions` shows the same list inside the TUI, with this run's id
marked so you can resume it later.

A resumed session gets its transcript and its history back. It does
not get the container: those checkpoints died with it, so a Turn from
before the restart is not offered for undo.

## Logs

One JSONL stream per session in `~/.local/state/detent/logs/`

```sh
jq 'select(.turn == "01a0…")'              one request, end to end
jq 'select(.event | startswith("call."))'  every tool call
jq 'select(.level == "WARN")'              what went wrong
```

## Architecture

Everything goes on one bus. Facts are what happened and comes from the engine, whereas intents are imperative and come from anyone.

```
         ┌────────── intents ──────────┐
         ↓                             │
      engine ──── facts ────→ bus ────→├→ ui
         │                             ├→ logging
         ↓                             ├→ judge     (how it went)
  tool, model, routing                 └→ viewgen   (how to draw it)
         ↓
  host  (unsandboxed)
  sandbox (containerd)
```

`ui` imports nothing under `internal/`, both sides import `event`, which depends on the standard library and `viewspec`

All extension listens, publishes, or both

[CLAUDE.md](CLAUDE.md) has how the code is arranged and why.
