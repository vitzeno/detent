package propose

import (
	"context"
	"fmt"
	"strings"
)

// MaxSummaryBytes caps what a summary may cost. A summary that grew
// without bound would defeat the compaction that asked for it.
const MaxSummaryBytes = 2 * 1024

// summarySystemPrompt asks for the facts a later command depends on,
// not a readable account of the session. What the model needs from
// dropped turns is what is now true of the machine.
const summarySystemPrompt = `You are compacting a shell session's history.

Write a terse factual record of what the earlier commands established, for
another model that will propose the next command and cannot see them.

Keep: files and directories created, moved or deleted; installed or missing
tools and their versions; configuration discovered; what failed and the exact
error; decisions already made.

Drop: narration, restated command output, anything already superseded.

Reply with plain prose under 200 words. No preamble, no headings, no JSON.`

// Summarize condenses messages into a short factual record. It
// satisfies agent.Summarizer.
func (p *OpenAIProposer) Summarize(ctx context.Context, messages []Message) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}
	msgs := []wireMessage{
		{Role: "system", Content: summarySystemPrompt},
		{Role: "user", Content: transcriptText(messages)},
	}
	// Free-form: the proposal schema would force this into a command.
	content, _, err := p.complete(ctx, msgs, nil)
	if err != nil {
		return "", fmt.Errorf("propose: summarize: %w", err)
	}
	return truncateStr(strings.TrimSpace(content), MaxSummaryBytes), nil
}

// transcriptText flattens turns into one labelled block, so the whole
// history arrives as material to compact rather than as a conversation
// the model might try to continue.
func transcriptText(messages []Message) string {
	var b strings.Builder
	for _, m := range messages {
		label := "user"
		switch m.Role {
		case RoleAssistant:
			label = "proposed"
		case RoleTool:
			label = "output"
		}
		fmt.Fprintf(&b, "[%s] %s\n", label, m.Content)
	}
	return b.String()
}
