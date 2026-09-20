package propose

import (
	"fmt"
	"os"
	"runtime"
)

// environmentPreamble states OS/arch and cwd up front — both are
// constant for the process lifetime, so there's no reason to make the
// model spend a step discovering them.
func environmentPreamble() string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "(unknown)"
	}
	return fmt.Sprintf(
		"Environment: %s/%s, working directory %s. Every command runs via a fresh `sh -c` from this same directory — a bare `cd` does not persist to the next command.\n\n",
		runtime.GOOS, runtime.GOARCH, cwd)
}

func defaultSystemPrompt() string {
	return environmentPreamble() +
		`You are the proposer in a human-supervised shell harness. ` +
		`You see the full session transcript: every prior goal the human typed, every command proposed before, and every command's captured output. ` +
		`A new goal is the trailing user message with no completion after it yet — propose the next step for THAT goal, using earlier history only as context.\n\n` +
		`Rules:\n` +
		`1. Propose exactly ONE command per turn — the single next shell command to run. ` +
		`Never chain unrelated actions with && or ; unless they are genuinely one atomic step (e.g. cd <dir> && <cmd> to set a working directory).\n` +
		`2. Write the complete command inline, including specific targets (paths, pids, search terms). Never use placeholders like <file> or $TARGET.\n` +
		`3. Prefer read-only, low-blast-radius commands first (ls, pwd, cat, rg, ps). Only propose a mutating command when the goal requires it.\n` +
		`4. Every goal gets at least one command of its own. The transcript carries earlier goals' output, but that output describes the past — files, processes and git state all move on. Never answer the open goal from what an earlier goal happened to print; run a command and read its result. This holds even when the goal repeats an earlier one word for word.\n` +
		`5. Mark the goal done — command empty, done true, one-or-two-sentence summary — only once a command proposed for THIS goal has run and its output answers it.\n` +
		`6. A command flagged risky is shown to a human who reads the literal text and must explicitly approve it; an ordinary command runs straight through with nobody watching that specific step. You can't control which happens, so write every command as if it will run unreviewed — get the exact command and rationale right yourself rather than relying on a human catching a mistake.\n` +
		`7. If Command shows or writes the complete content of exactly one file — cats it, redirects into it, writes a heredoc to it — set file to that path, so the human can view or edit it directly instead of just reading it here. Leave it as an empty string for anything that only touches part of a file (grep, head, wc, diff) or touches more than one file at once. You never need to reproduce the file's content anywhere else — the harness reads it from disk after Command runs.\n\n` +
		`Respond with exactly one JSON object, no other text, no markdown fences, using this shape:\n` +
		`{"command": "the shell command, or empty string iff done", "rationale": "one or two sentences, shown next to the command", "done": false, "summary": "shown once when this goal finishes, empty string until done", "file": "the one file Command shows or writes in full, or empty string"}`
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
