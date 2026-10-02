# detent

A terminal agent where commands run in a container and every request is checkpointed so you can undo it

Everything is an event on a single bus and every part of detent is a subscriber

Nothing calls the engine itself including logging and the TUI. They just listen and write what they hear, same with the SQLite log that makes a session resumable, as is the judge that decides how a call went

If any of them are removed everything else runs unchanged

Future extensions are expected to subscribe and publish to the event bus too

Needs an OpenAI-compatible `/chat/completions` endpoint with tool calling.
LM Studio, OpenRouter and OpenAI all work

```sh
go install github.com/vitzeno/detent/cmd/detent@latest

make build && ./bin/detent            # sandboxed, needs containerd
make install                          # onto your PATH
./bin/detent -sandbox host            # no container
./bin/detent -prompt "find go files over 1MB"
```

Headless runs leave MCP out: no config is read and no server is started. MCP is TUI only

## Architecture

Events are split into facts and intents, facts for what happened, intents for what someone wants to happen

Either can come from anyone. The engine publishes most facts, but the store, the judge and MCP publish their own

```
  publishes                   bus      subscribes
                               │
  engine ───── facts ───────→  │  ──→  engine     intents only
    │                          │  ──→  ui         draws, publishes back intent (e.g. new prompt)
    ├── tool                   │  ──→  headless   one request, no TUI
    ├── model                  │  ──→  logging    the JSONL stream
    └── routing                │  ──→  store      SQLite allows resume
          ├── host             │  ──→  judge      how the Call went
          └── sandbox          │  ──→  viewgen    how to draw it
                               │
  anyone ───── intents ─────→  │
```

`ui` imports nothing under `internal/`, both sides import `event`, which depends on the standard library and `viewspec`

All extensions listen, publish, or both

## Configuration

`./.detent.yaml` or `~/.config/detent/config.yaml`

All keys are documented in [`detent.example.yaml`](detent.example.yaml)

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

A remote server that answers 401 gets a sign-in link in the output pane. `o` opens
it, `c` copies it, and the token is kept in `~/.local/state/detent/mcp/` so the
next launch does not ask again. Claude Code's `oauth` object (`clientId`,
`clientSecret`, `callbackPort`, `scopes`) is read for providers that need it.
`/mcp auth <server>` signs in again

These calls run in detent's process, not the container, and no checkpoint
undoes one. So every MCP call is confirmed, whatever the server says about
itself

## Skills

A skill is a folder with a `SKILL.md` in it: a name, a description, and instructions for one kind of task. They follow the [Agent Skills](https://agentskills.io) format, so ones written for Claude Code, Codex or Cursor work here as they are

```
.agents/skills/release/
├── SKILL.md
└── scripts/tag.sh
```

detent looks in `.agents/skills/` and `.claude/skills/` from the git root down, then in `~/.agents/skills/` and `~/.claude/skills/`. If two share a name, the project's wins

The model only sees names and descriptions until a request fits one, then it loads that skill. You can ask for one yourself with `/release cut v2.1`, and `/skills` lists what was found

`disable-model-invocation: true` keeps a skill for you to call by hand, `user-invocable: false` leaves it to the model

Scripts in a skill run like any other command, through the same approvals. In the sandbox your own skills are mounted read-only

## Instructions

detent reads the instruction files a project keeps for coding agents and adds them to the model's prompt

`AGENTS.md`, or `CLAUDE.md` where a directory has no `AGENTS.md`, in each directory from the git root down to where you started detent. The nearest one wins where they disagree

`~/.config/detent/AGENTS.md` is your own, read first in every project

Together they are capped at 128KB, about 32k tokens, cutting the outermost file first. That suits a large-window model, but on a small local one the files can take most of the context, so keep them short there. `/status` lists which were read

## Tools

| Tool         | Does                              |
| ------------ | --------------------------------- |
| `bash`       | runs a command                    |
| `read_file`  | reads a file, a window at a time  |
| `write_file` | writes a whole file               |
| `edit_file`  | replaces an exact piece of a file |
| `list_dir`   | lists a directory                 |
| `grep`       | searches file contents            |
| `find_files` | finds files by name               |
| `web_search` | searches the web                  |
| `skill`      | loads a skill, when there are any |

Each one is a shell command underneath, so it runs in the sandbox like everything else and goes through the same checks. The read-only ones run together

`edit_file` and `write_file` come back as a diff, drawn side by side when the pane is wide enough

A command still running after 10 minutes is stopped, and the model is told so along with what it printed. `command_timeout` in the config changes it

`web_search` is a curl to DuckDuckGo so there is no API key but it needs the sandbox to have network, which is the default

## Your own commands

shift+tab switches the input bar between a request and a shell. The border
turns amber and the prompt becomes `$`. `/shell` does the same thing

What you type goes to the runner the agent uses, so in the sandbox you are
looking at what it just did. Output streams into the pane like any other
row, esc stops it

Nothing flags it and nothing asks you to approve it

It gets the same views as the agent's commands. With `views: generate`, Jev is
asked what shape the output is, never whether it went well

The model reads it afterwards, as a message in the transcript

```
[human ran a command in the sandbox]
$ git status --short
exit 0
 M ui/keys.go
```

So it knows what you checked instead of checking again

## Undoing

`/undo 2` restores the container to before your second request and trims the
transcript to match

Commands you ran yourself go back with it, if they ran after that snapshot

Your working directory is mounted at `/workspace`, outside the snapshot

## Sandboxing (**Experimental**)

```sh
brew install colima
colima start --cpu 6 --memory 8 --disk 30 --kubernetes=false
```

On Linux, run containerd and point `sandbox_socket` at it.

A session gets its own container. Resuming one clears whatever the last
process left behind, but a session you never come back to keeps its
container and snapshot. `./bin/detent -prune` drops those, and leaves
alone anything still running.

## Approving

A Call runs without asking unless it is flagged dangerous, you see the
literal command and answer it.

Flagging is a chain: the tool's own declared mutability, a regex backstop, a
repeat check, then TypeSafe's Jev if a key is set. Each link can raise the
verdict but none can lower it

Declining stops that Call, the agent reads the refusal and tries
something else

Typing while it works steers it

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
not get the container: those checkpoints died with it, so a request from
before the restart is not offered for undo.

`/status` says whether anything is recording the session at all. If
nothing is, it cannot be resumed, and it is better to know while you
are working than when you try.

## Generative Output (**Experimental**)

The output pane draws from a view spec: a parse for reading bytes into rows,
blocks for drawing them

Currently thirty widgets over nine parse kinds, including
gauges, histograms, box plots, gantt charts, braille scatter plots and
heatmaps

With `views: generate` and a Jev key it designs a spec for output nothing
covers

Jev answers closed questions rather than writing JSON, so it cannot
name a widget or field that does not exist

All takes about 600ms, then cached

## Logs

One JSONL stream per session in `~/.local/state/detent/logs/`

```sh
jq 'select(.turn == "01a0…")'              one request, end to end
jq 'select(.event | startswith("call."))'  every tool call
jq 'select(.level == "WARN")'              what went wrong
```

[CLAUDE.md](CLAUDE.md) has how the code is arranged and why.

Hello, world!
