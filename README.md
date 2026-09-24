# detent

A terminal agent where commands run in a container and every request is checkpointed so you can undo it

Everything is an event on a single bus and every part of detent is a subscriber

Nothing calls the engine itself including logging and the TUI. They just listen and write what they hear, same with the SQLite log that makes a session resumable, as is the judge that decides how a call went

If any of them are removed everything else runs unchanged

Future extensions are expected to subscribe and publish to the event bus too

Needs an OpenAI-compatible `/chat/completions` endpoint with tool calling.
LM Studio, OpenRouter and OpenAI all work

```sh
make build && ./bin/detent            # sandboxed, needs containerd
make install                          # onto your PATH
./bin/detent -sandbox host            # no container
./bin/detent -prompt "find go files over 1MB"
```

## Architecture

Events are split into facts and intents, facts for what happened, intents for what someone wants to happen

Facts only come from the engine, intents can come from anyone

```
  publishes                   bus      subscribes
                               │
  engine ───── facts ───────→  │  ──→  engine     intents only
    │                          │  ──→  ui         draws, publishes back intent (e.g. new prompt)
    ├── tool                   │  ──→  headless   one Turn, no TUI
    ├── model                  │  ──→  logging    the JSONL stream
    └── routing                │  ──→  store      SQLite allows resume
          ├── host             │  ──→  judge      how the Call went
          └── sandbox          │  ──→  viewgen    how to draw it
                               │
  anyone ───── intents ─────→  │
```

`ui` imports nothing under `internal/`, both sides import `event`, which depends on the standard library and `viewspec`

All extensions listen, publish, or both

## Turns, Steps and Calls

|      | Is                                       | Unit of                 |
| ---- | ---------------------------------------- | ----------------------- |
| Turn | your prompt and everything done about it | undo, one history block |
| Step | one model round trip                     | the transcript's atom   |
| Call | one tool invocation                      | one row, one approval   |

A Step can ask for several Calls and runs the read-only ones together. A Turn
runs Steps until the model stops asking for tools, capped at 50. The cap asks
whether to continue rather than stopping

Current Tools: `bash`, `read_file`, `write_file`, `list_dir`, `web_search`.

`web_search` is just curl so no API key exists, it needs the sandbox to have network, which is the default

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

A session gets its own container. Resuming one clears whatever the last
process left behind, but a session you never come back to keeps its
container and snapshot. `./bin/detent -prune` drops those, and leaves
alone anything still running.

## MCP

Servers go in `.mcp.json`, pretty much the standard so one written for another client works here

`~/.config/detent/mcp.json` and `./.mcp.json` merge, nearest wins
Also `${VAR}` expands from the environment, so a committed file can name a token it does not hold

```json
{
	"mcpServers": {
		"github": {
			"command": "docker",
			"args": ["run", "-i", "--rm", "ghcr.io/github/github-mcp-server"]
		},
		"linear": { "type": "http", "url": "https://mcp.linear.app/mcp" }
	}
}
```

`./bin/detent -mcp` connects and lists what each offers. `/mcp` shows the
same from inside

These calls run in detent's process, not the container, and no checkpoint
undoes one. So every MCP call is confirmed, whatever the server says about
itself

## Configuration

`./.detent.yaml` or `~/.config/detent/config.yaml`

All keys are documented in [`detent.example.yaml`](detent.example.yaml)

## Resuming

Every session is recorded to `~/.local/state/detent/events.db`, so it
can be replayed rather than reconstructed.

```sh
detent -sessions                    # what can be resumed
detent -resume last                 # continue the most recent
detent -resume <id>                 # or a specific one
detent -resume "the sandbox bug"    # or one you named
```

Inside the TUI, `/sessions` shows the same list with this run marked,
and `/rename <name>` names it so the list is not a wall of ids. A name
has to be one `-resume` can reach, so `last`, anything uuid-shaped,
and a name another session already has are all refused when you set
them.

A resumed session gets its transcript and its history back. It does
not get the container: those checkpoints died with it, so a Turn from
before the restart is not offered for undo.

`/status` says whether anything is recording the session at all. If
nothing is, it cannot be resumed, and it is better to know while you
are working than when you try.

## Logs

One JSONL stream per session in `~/.local/state/detent/logs/`

```sh
jq 'select(.turn == "01a0…")'              one request, end to end
jq 'select(.event | startswith("call."))'  every tool call
jq 'select(.level == "WARN")'              what went wrong
```

[CLAUDE.md](CLAUDE.md) has how the code is arranged and why.
