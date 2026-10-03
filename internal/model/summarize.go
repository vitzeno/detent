package model

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/vitzeno/detent/event"
)

// MaxSummaryBytes caps what a summary costs. One that grew unbounded
// would defeat the compaction that asked for it.
const MaxSummaryBytes = 2 * 1024

// Bounds on what the summarizer is shown. Compaction runs when the window
// is nearly full, so the request asking for a summary must not overflow it.
const (
	maxResultBytes  = 2 * 1024
	maxArgBytes     = 200
	maxSummaryInput = 96 * 1024
)

// Summarize condenses Steps into a factual record, for compaction.
func (c *Client) Summarize(ctx context.Context, msgs []event.Message) (string, error) {
	if len(msgs) == 0 {
		return "", nil
	}
	req := []event.Message{
		{Role: event.RoleSystem, Content: summaryPrompt},
		{Role: event.RoleUser, Content: clip(transcriptText(msgs), maxSummaryInput)},
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
	// Reasoning, or a reply cut off, is not a record of what is true.
	if reply.Unfinished() {
		return "", errors.New("model: summary did not finish")
	}
	out := strings.TrimSpace(reply.Text)
	if len(out) > MaxSummaryBytes {
		out = strings.ToValidUTF8(out[:MaxSummaryBytes], "")
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

// transcriptText flattens Steps for the summarizer, with each argument
// and result cut short, since a summary needs what happened, not the bytes.
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
			for _, c := range m.Requests {
				fmt.Fprintf(&b, "Agent ran %s(%s)\n", c.Name, argsText(c.Args))
			}
		case event.RoleTool:
			fmt.Fprintf(&b, "Result: %s\n", clip(m.Content, maxResultBytes))
		case event.RoleSystem:
			// The prompt is not part of what happened.
		}
	}
	return b.String()
}

// argsText is sorted, so the same history always reads the same.
func argsText(args map[string]any) string {
	parts := make([]string, 0, len(args))
	for _, k := range slices.Sorted(maps.Keys(args)) {
		v := fmt.Sprint(args[k])
		if len(v) > maxArgBytes {
			v = strings.ToValidUTF8(v[:maxArgBytes], "") + "…"
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}

// clip keeps the head and tail of s within n bytes, where a command's
// outcome and its error usually are.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	half := n / 2
	return strings.ToValidUTF8(s[:half], "") + "\n…[cut]…\n" + strings.ToValidUTF8(s[len(s)-half:], "")
}
