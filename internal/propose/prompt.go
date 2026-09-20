package propose

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// Environment describes where the proposed commands actually run. The
// harness fills this in; without it the prompt would describe the
// process doing the proposing, which stops being true the moment
// commands run in a container.
type Environment struct {
	OS   string
	Arch string
	Dir  string // working directory each command starts in

	// Sandboxed is true when commands run in a container rather than
	// on the human's own machine.
	Sandboxed bool
	// Network is true when the environment can reach the network.
	Network bool
	// Undoable is true when each step is checkpointed and the human
	// can roll one back.
	Undoable bool
}

// LocalEnvironment describes this process's own machine: the right
// answer when commands run unsandboxed, and the fallback when the
// harness says nothing.
func LocalEnvironment() Environment {
	dir, err := os.Getwd()
	if err != nil {
		dir = "(unknown)"
	}
	// Network, because the machine detent itself runs on has one. Not
	// Undoable: nothing checkpoints the user's own filesystem.
	return Environment{OS: runtime.GOOS, Arch: runtime.GOARCH, Dir: dir, Network: true}
}

// preamble states the facts a command depends on: which OS's flags to
// use, where it starts, what survives, and what can be undone.
func (e Environment) preamble() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Environment: %s/%s, working directory %s.\n", e.OS, e.Arch, e.Dir)

	if e.Sandboxed {
		b.WriteString("Commands run in a container, not on the human's own machine. " +
			"The working directory is their real project directory, mounted in: changes there are real and are not undone. " +
			"Everything outside it belongs to the container and is discarded when the session ends.\n")
	} else {
		b.WriteString("Commands run directly on the human's own machine, against their real files.\n")
	}

	if e.Network {
		b.WriteString("The network is reachable, so fetching and installing work.\n")
	} else {
		b.WriteString("There is no network: nothing can be fetched, cloned or installed. Work with what is already here.\n")
	}

	if e.Undoable {
		b.WriteString("Each step is checkpointed, and the human can undo a step and everything after it.\n")
	}

	b.WriteString("Each command runs through a fresh `sh -c` starting in that directory. " +
		"A bare `cd` does not carry to the next command, but files you create or change do, and so does anything you install.\n\n")
	return b.String()
}

func defaultSystemPrompt(env Environment) string {
	return env.preamble() +
		`You are the proposer in a human-supervised shell harness. You work toward one goal at a time by proposing single shell commands and reading what they print.

You see the whole session: every goal the human has typed, every command proposed, and every command's output. The open goal is the trailing user message with nothing after it yet.

Working the goal:
1. Propose exactly ONE command per turn. Don't chain unrelated actions with && or ; unless they are genuinely one step (cd <dir> && <cmd> to set a working directory).
2. Write the command complete: real paths, real pids, real search terms. Never a placeholder like <file> or $TARGET.
3. Every goal gets at least one command of its own. Earlier goals' output is in the transcript, but it describes the past — files, processes and git state have moved on since. Never answer the open goal from what an earlier goal printed, even when the goal repeats an earlier one word for word.
4. Set done only once a command run for THIS goal has printed something that answers it. Then leave command empty and write a one-or-two-sentence summary.
5. Build on what already ran. The filesystem carries your earlier steps, so don't redo setup the transcript shows you already did.

Choosing the command:
6. Write for the environment named above, not the one you might assume — flags differ between Linux and macOS.
7. Reach for read-only commands first (ls, cat, rg, ps, git status). Propose something that writes or deletes when the goal actually needs it.
8. A risky command is shown to the human, who reads it and must approve it; an ordinary one runs straight through with nobody watching that step. You don't control which, so write every command as though nobody will look.

Reporting:
9. If the command shows or writes one whole file — cats it, redirects into it, heredocs to it — put that path in file, and the human can open it directly. Leave file empty for anything touching part of a file (grep, head, diff) or more than one. Never paste the file's content anywhere: the harness reads it from disk.

Reply with exactly one JSON object, no other text and no markdown fences:
{"command": "the shell command, or empty string iff done", "rationale": "one or two sentences, shown next to the command", "done": false, "summary": "shown once when this goal finishes, empty string until done", "file": "the one file the command shows or writes in full, or empty string"}`
}

// responseFormat fixes the proposal shape as a strict JSON schema.
// LM Studio's server rejects {"type": "json_object"} — it only accepts
// "json_schema" or "text" — so the schema form is the portable choice.
func responseFormat() map[string]any {
	return map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "proposal",
			"strict": true,
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command":   map[string]any{"type": "string"},
					"rationale": map[string]any{"type": "string"},
					"done":      map[string]any{"type": "boolean"},
					"summary":   map[string]any{"type": "string"},
					"file":      map[string]any{"type": "string"},
				},
				"required":             []string{"command", "rationale", "done", "summary", "file"},
				"additionalProperties": false,
			},
		},
	}
}
