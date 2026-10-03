package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vitzeno/detent/internal/capture"
)

// toResult flattens a tool result into what every tool call reports. The
// transcript is text, so a non-text block becomes a line about it.
func toResult(res *sdk.CallToolResult) capture.Result {
	var b strings.Builder
	for _, c := range res.Content {
		line := describe(c)
		if line == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(line)
	}
	// Servers should duplicate structured output into a text block.
	if b.Len() == 0 && res.StructuredContent != nil {
		if raw, err := json.Marshal(res.StructuredContent); err == nil {
			b.Write(raw)
		}
	}

	text, truncated := bound(b.String())
	out := capture.Result{Stdout: text, Truncated: truncated}
	// isError is the model's cue to correct itself, so it reads as a
	// failure the way a non-zero exit does.
	if res.IsError {
		out.ExitCode = 1
	}
	return out
}

func describe(c sdk.Content) string {
	switch v := c.(type) {
	case *sdk.TextContent:
		return v.Text
	case *sdk.ImageContent:
		return fmt.Sprintf("[image %s, %s]", orUnknown(v.MIMEType), size(len(v.Data)))
	case *sdk.AudioContent:
		return fmt.Sprintf("[audio %s, %s]", orUnknown(v.MIMEType), size(len(v.Data)))
	case *sdk.ResourceLink:
		return fmt.Sprintf("[resource %s]", v.URI)
	case *sdk.EmbeddedResource:
		return embedded(v)
	}
	return fmt.Sprintf("[%T]", c)
}

// embedded prefers the text: a server that inlines a file meant the
// model to read it, not to be told it exists.
func embedded(v *sdk.EmbeddedResource) string {
	if v.Resource == nil {
		return "[resource]"
	}
	if v.Resource.Text != "" {
		return fmt.Sprintf("[resource %s]\n%s", v.Resource.URI, v.Resource.Text)
	}
	return fmt.Sprintf("[resource %s, %s]", v.Resource.URI, size(len(v.Resource.Blob)))
}

// bound caps a result and says so: losing the tail silently is how a
// model reasons off a lie.
func bound(s string) (string, bool) {
	if len(s) <= capture.MaxOutputBytes {
		return s, false
	}
	return cut(s, capture.MaxOutputBytes), true
}

func size(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown type"
	}
	return s
}
