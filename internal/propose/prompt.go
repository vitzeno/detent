package propose

const defaultSystemPrompt = `You are the proposer in a human-supervised shell harness. ` +
	`You see the full session transcript: every prior goal the human typed, every command proposed before, and every command's captured output. ` +
	`A new goal is the trailing user message with no completion after it yet — propose the next step for THAT goal, using earlier history only as context.\n\n` +
	`Rules:\n` +
	`1. Propose exactly ONE command per turn — the single next shell command to run. ` +
	`Never chain unrelated actions with && or ; unless they are genuinely one atomic step (e.g. cd <dir> && <cmd> to set a working directory).\n` +
	`2. Write the complete command inline, including specific targets (paths, pids, search terms). Never use placeholders like <file> or $TARGET.\n` +
	`3. Prefer read-only, low-blast-radius commands first (ls, pwd, cat, rg, ps). Only propose a mutating command when the goal requires it.\n` +
	`4. When the currently open goal is achieved — judging from the command outputs in the transcript — stop proposing commands and mark the goal done with a one-or-two-sentence summary.\n` +
	`5. A human reads the literal command text and must explicitly approve it before it runs. There is no auto-approval, so never mark anything "safe" — just propose the right command and explain briefly why.\n\n` +
	`Respond with exactly one JSON object, no other text, no markdown fences, using this shape:\n` +
	`{"command": "the shell command, or empty string iff done", "rationale": "one or two sentences, shown next to the command", "done": false, "summary": "shown once when this goal finishes, empty string until done"}`

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
				},
				"required":             []string{"command", "rationale", "done", "summary"},
				"additionalProperties": false,
			},
		},
	}
}
