# detent

A terminal agent you stay in front of. It calls tools until it has an answer,
asks before anything risky, and checkpoints each request so you can undo it.

Needs an OpenAI-compatible `/chat/completions` endpoint with tool calling.
LM Studio, OpenRouter and OpenAI all work.

```sh
make build && ./bin/detent            # sandboxed, needs containerd
make install                          # onto your PATH
./bin/detent -sandbox host            # no container
./bin/detent -prompt "find go files over 1MB"
```

## Turns, Steps and Calls

| | Is | Unit of |
| --- | --- | --- |
| Turn | your prompt and everything done about it | undo, one history block |
| Step | one model round trip | the transcript's atom |
| Call | one tool invocation | one row, one approval |

A Step can ask for several Calls and runs the read-only ones together. A Turn
runs Steps until the model stops asking for tools, capped at 50. The cap asks
whether to continue rather than stopping.

Tools: `bash`, `read_file`, `write_file`, `list_dir`.

## Approving

A Call runs without asking unless it is flagged dangerous. Then you see the
literal command and answer it.

Flagging is a chain: the tool's own declared mutability, a regex backstop, a
repeat check, then TypeSafe's Jev if a key is set. Each link can raise the
verdict and none can lower it.

Declining stops that Call and not the Turn. The agent reads the refusal, tries
something else, and says what it skipped.

Typing while it works steers it. The next Step sees what you wrote.

## Undoing

`/undo 2` restores the container to before your second request and trims the
transcript to match. One checkpoint per Turn, taken before anything ran.

Your working directory is bind-mounted at `/workspace`, outside the snapshot.
detent asks separately before reverting those files and the default is to
leave them.

## Output

The output pane draws from a view spec: a parse for reading bytes into rows,
blocks for drawing them. Thirty widgets over eight parse kinds, including
gauges, histograms, box plots, gantt charts, braille scatter plots and
heatmaps.

With `views: generate` and a Jev key it designs a spec for output nothing
covers. It answers closed questions rather than writing JSON, so it cannot
name a widget or field that does not exist. About 600ms, then cached per
command shape.

The model writes a regexp and picks widgets. The interpreter runs that regexp
over real output, so a wrong spec drops the view instead of inventing a value.

## Keys

| Key | Does |
| --- | --- |
| `enter` | send the input box |
| `alt+enter`, `ctrl+j` | newline |
| `tab` | cycle input, history, output |
| `esc` | abort the request, close a panel, or step back |
| `ctrl+c` | quit |

With the cursor in history or the output pane:

| Key | Does |
| --- | --- |
| `↑` `↓` | move through history, or scroll and move the view's selection |
| `pgup` `pgdn` | page |
| `space`, `v` | expand a row |
| `enter` | put the selected row into the prompt, else expand |

Answering a flagged command, or the step cap:

| Key | Does |
| --- | --- |
| `y`, `enter` | run it, or keep going |
| `n` | skip the Call, or stop |

Answering an undo, where the safe answer is the one `enter` takes:

| Key | Does |
| --- | --- |
| `n`, `enter` | roll the container back, leave your files |
| `y` | revert your files as well |
| `esc` | cancel |

`shift+enter` inserts a newline if your terminal sends `\x1b\r` for it.

Commands: `/status`, `/usage`, `/undo`, `/new`, `/abort`, `/help`, `/quit`.
Type `/` for the list. `↑` `↓` pick, `tab` completes, `enter` runs.

Themes are `dark`, `light`, `solarized` and `dracula`, via `theme:` or
`DETENT_THEME`.

## Sandboxing

```sh
brew install colima
colima start --cpu 6 --memory 8 --disk 30 --kubernetes=false
```

The runtime does not matter, colima forwards a containerd socket either way.
Kubernetes does: k3s costs about a core and detent never uses it.

On Linux, run containerd and point `sandbox_socket` at it.

## Configuration

`./.detent.yaml` or `~/.config/detent/config.yaml`. Every key is documented in
[`detent.example.yaml`](detent.example.yaml). Flags beat env beats file.

`DETENT_BASE_URL`, `DETENT_MODEL`, `DETENT_API_KEY`, `TYPESAFE_API_KEY`,
`DETENT_VIEWS`, `DETENT_SANDBOX_MODE`, `DETENT_LOG_LEVEL`. A repo-local `.env`
is loaded too.

## Logs

One JSONL stream per session in `~/.local/state/detent/logs/`. One file rather
than one per component, because a step crosses several of them.

```sh
jq 'select(.turn == "01a0…")'              one request, end to end
jq 'select(.event | startswith("call."))'  every tool call
jq 'select(.level == "WARN")'              what went wrong
```

`seq` is gapless, so a jump means a record was filtered rather than lost.

## Architecture

Everything goes on one bus. Facts are past tense and come from the engine;
intents are imperative and come from anyone.

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

`ui` imports nothing under `internal/`. Both sides import `event`, which
depends on the standard library and `viewspec`. There is no translation layer
because there is nothing to translate.

An extension listens, publishes, or both. There is no second mechanism.

[CLAUDE.md](CLAUDE.md) has how the code is arranged and why.
