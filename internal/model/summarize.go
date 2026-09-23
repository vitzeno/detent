package model

import (
	"context"
	"fmt"
	"github.com/vitzeno/detent/event"
	"strings"
)

// MaxSummaryBytes caps what a summary costs. One that grew unbounded
// would defeat the compaction that asked for it.
const MaxSummaryBytes = 2 * 1024

// Summarize condenses Steps into a factual record, for compaction.
func (c *Client) Summarize(ctx context.Context, msgs []event.Message) (string, error) {
	if len(msgs) == 0 {
		return "", nil
	}
	req := []event.Message{
		{Role: event.RoleSystem, Content: summaryPrompt},
		{Role: event.RoleUser, Content: transcriptText(msgs)},
	}
	wire := make([]wireMessage, 0, len(req))
	for _, m := range req {
		wire = append(wire, encode(m))
	}
	// No tools: the point is prose.
	reply, _, err := c.send(ctx, wireRequest{Messages: wire, Temperature: 0.2})
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(reply.Text)
	if len(out) > MaxSummaryBytes {
		out = out[:MaxSummaryBytes]
	}
	return out, nil
}

// summaryPrompt asks for what is now true of the machine, not a
// readable account of the session.
const summaryPrompt = `You are compacting a shell session's history.

Write a terse factual record of what the earlier steps established, for
another model that will continue the work and cannot see them.

Keep: files and directories created, moved or deleted; installed or missing
tools and their versions; configuration discovered; what failed and the exact
error; decisions already made.

Drop: narration, restated command output, anything already superseded.

Reply with plain prose under 200 words. No preamble, no headings, no JSON.`

// transcriptText flattens Steps for the summarizer, naming tool calls
// rather than restating their arguments.
func transcriptText(msgs []event.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case event.RoleUser:
			fmt.Fprintf(&b, "Human: %s\n", m.Content)
		case event.RoleAssistant:
			if m.Content != "" {
				fmt.Fprintf(&b, "Agent: %s\n", m.Content)
			}
			for _, c := range m.Calls {
				fmt.Fprintf(&b, "Agent ran %s(%s)\n", c.Name, argsText(c.Args))
			}
		case event.RoleTool:
			fmt.Fprintf(&b, "Result: %s\n", m.Content)
		}
	}
	return b.String()
}

func argsText(args map[string]any) string {
	parts := make([]string, 0, len(args))
	for k, v := range args {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(parts, " ")
}
