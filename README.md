# detent

An agent at your terminal, with you still in the loop.

You ask for something. The model calls tools until it has an answer — reading
files, listing directories, running shell commands, several at once when they
are independent. Risky commands stop and ask you. Every request is
checkpointed, so you can undo the whole thing.

Works with any OpenAI-compatible `/chat/completions` endpoint that supports
tool calling: LM Studio, OpenRouter, OpenAI.

![detent](screenshots/tui-0-dark.png)

```sh
make build && ./bin/detent          # sandboxed (needs containerd, see below)
make install                        # and onto your PATH
./bin/detent -sandbox host          # straight on your machine
./bin/detent -prompt "find go files over 1MB"
```

---

## What it does

| | |
| --- | --- |
| **Tools, not one command** | `bash`, `read_file`, `write_file`, `list_dir`. Read-only ones run together. |
| **Confirm only when it matters** | A hook chain flags the dangerous ones — each tool's own floor, a regex backstop, a repeat check, then TypeSafe's Jev. Everything else runs through. |
| **Declining stops a call, not the request** | Say no and the agent reads that, tries something else, and tells you what it skipped. |
| **Undo** | `/undo 2` restores the container to before your second request and trims the transcript to match. |
| **Steering mid-request** | Type while it works and the next step sees it. Correcting costs nothing. |
| **Sandboxed by default** | One containerd container per session. Your working dir is bind-mounted at `/workspace`. |
| **Output that reads** | The pane picks a rendering per output shape, and can design one it has never seen. |
| **A log you can query** | One JSONL file per session, every record tagged with the turn, step and call it belongs to. |

## How a request runs

Three scopes, and the names matter because everything is built on them:

| | Is | Unit of |
| --- | --- | --- |
| **Turn** | your prompt and everything the agent did about it | **undo**, one block in history |
| **Step** | one model round trip | the transcript's atom |
| **Call** | one tool invocation | one row, one approval |

A Step may ask for several Calls; a Turn runs Steps until the model stops
asking for tools. Steps are never shown — you do not think in model round
trips.

Fifty Steps per Turn by default, and that bound is soft: hitting it asks
whether to keep going rather than stopping dead. The judge can also decide the
request looks answered and ask the loop to stop early.

## Reading the output

The pane draws from a *view spec*: a parse saying how to read the bytes into
rows, and blocks saying how to draw them. Thirty widgets, eight parse kinds:
tables and trees, but also gauges, histograms, box plots, stacked bands, gantt
charts, braille scatter plots and heatmaps. Detent ships specs for common
commands and picks a sensible default otherwise.

Set `views: generate` and it will design one for output nothing covers, by
asking the judge a handful of closed questions: where the header is, how to
read the bytes, which widget draws them, which field each one reads. It never
writes the spec, it picks from lists, so it cannot name a widget or a field
that does not exist. About 600ms, and the second run of that command is free.

That needs a Jev key. Without one you still get every spec detent ships and
every one you have saved; you just never get a new one.

![generated view](screenshots/tui-0-dark-gen.png)

The header says where the framing came from, and only when a model had a hand
in it: `✦ composed` or `✦ saved`. Detent's own renderings say nothing.

**The model never writes your data.** It writes a regexp and picks widgets;
the interpreter runs that regexp over real output. A spec can point at the
wrong field, which drops the view. It cannot put a value on screen that the
command never printed.

## Undoing a request

`/undo 2` undoes your second request **and everything after it**. The Turn is
the unit: one checkpoint taken before anything ran, so there is no partial
state to reason about and no numbering to get wrong.

Your own files are a separate question. `/workspace` is a bind mount, not part
of the snapshot, so detent asks before touching them. The default answer is the
safe one: roll the container back, leave your files.

## Safety, stated plainly

- Commands run straight through unless flagged **Dangerous**, which shows the
  literal text and waits for you.
- The chain that decides can only ever *widen* a verdict. Answers fold with a
  max, so a hook cannot soften a confirm even if it returns one saying so.
- Without a Jev key, only the regex backstop and each tool's own floor apply.
- `bash` is not privileged: same registry, same schema, same chain. The tools
  exist because they are cheaper and more legible, not because they restrict
  anything.
- The session bar always says where commands run: `sandbox ●` or
  `host ⚠ unsandboxed`.
- Colour carries status; prose never does. Anything the model wrote renders
  dimmed, so a confident sentence cannot look like a result.

![history and usage](screenshots/tui-2-dark.png)

## Keys

| Key | Does |
| --- | --- |
| `enter` | send what is in the input box |
| *(typing mid-request)* | steers: the next step sees it, no new request |
| `/` | command list: `/status`, `/usage`, `/undo`, `/new`, `/abort`, `/help`, `/quit` |
| `esc` | abort the running request, or back out of a pane |
| `tab` | cycle input → history → output |
| `↑` `↓` | move through history, or scroll the focused pane |
| `enter` *(output pane)* | act on the selected row, into the prompt |
| `space` | expand the focused row |
| `y` `n` | answer a confirm, or the step bound |
| `ctrl+c` | quit |

`alt+enter` or `ctrl+j` inserts a newline. For `shift+enter`, bind it in your
terminal to send `\x1b\r`; terminals send the same byte for both otherwise.

Paste works in the input box, newlines included.

## Themes

`dark`, `light`, `solarized`, `dracula` via `theme:` or `DETENT_THEME`.

![dracula](screenshots/tui-0-dracula.png)

## Sandboxing

```sh
brew install colima                                        # macOS
colima start --cpu 6 --memory 8 --disk 30 --kubernetes=false
```

The runtime does not matter — colima forwards a containerd socket either way.
Kubernetes does: k3s inside the VM costs about a core and detent never uses it.

On Linux, run containerd and point `sandbox_socket` at it. Undo restores
container state only: the workspace bind mount is deliberately outside the
snapshot. [CLAUDE.md](CLAUDE.md) has more, including how to keep a k8s cluster
and detent on the same machine.

## Configuration

`./.detent.yaml` or `~/.config/detent/config.yaml`. Every key is documented in
[`detent.example.yaml`](detent.example.yaml). Precedence: flags > env > file >
built-ins.

Common env vars: `DETENT_BASE_URL`, `DETENT_MODEL`, `DETENT_API_KEY`,
`TYPESAFE_API_KEY`, `DETENT_VIEWS`, `DETENT_SANDBOX_MODE`, `DETENT_LOG_LEVEL`.
A repo-local `.env` is loaded too.

## Logs

One JSONL stream per session at `~/.local/state/detent/logs/`. One file, not
one per component, because the thing you investigate is a step and a step
crosses several of them:

```sh
jq 'select(.turn == "01a0…")'              everything about one request
jq 'select(.event | startswith("call."))'  every tool call and how it went
jq 'select(.level == "WARN")'              what went wrong
```

`seq` is gapless per session, so a jump means a record was filtered rather
than lost.

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

`ui` imports nothing under `internal/`: both sides import `event`, which
depends on the standard library and `viewspec` and nothing else. There is no
translation layer, because there is nothing to translate.

An extension listens, publishes, or both. There is no second mechanism.

[CLAUDE.md](CLAUDE.md) has how the code is arranged and why.
