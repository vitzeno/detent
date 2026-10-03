package event

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// The transcript's vocabulary. Here rather than in the model client
// because a fact carries it, and this package may import neither side.

// Message is one transcript entry. An assistant message with tool calls and
// the tool messages answering it are one Step, and indivisible.
type Message struct {
	Role    Role
	Content string
	// Requests belong to an assistant message.
	Requests []ToolRequest
	// RequestID answers one call, and is set on RoleTool alone.
	RequestID string
}

// ToolRequest is one thing the model wants run. ID is what the answering
// message must carry back.
type ToolRequest struct {
	ID   string
	Name string
	Args map[string]any
	// Err is set when the model's arguments were not valid JSON. The
	// call still needs a result, or the next Step is malformed.
	Err string
}

// Role is who wrote a Message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Answer builds the tool message that closes one call.
func Answer(call ToolRequest, content string) Message {
	return Message{Role: RoleTool, RequestID: call.ID, Content: content}
}

// RequestIDs are what a Step's results must answer, in order.
func RequestIDs(calls []ToolRequest) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.ID
	}
	return out
}

// Command renders a call the way a human is shown it: bash as its own
// command, anything else as tool k=v, which truncates better than JSON.
// A value that could be misread is quoted, so k=v pairs never run together.
func Command(tool string, args map[string]any) string {
	if tool == "bash" {
		if c, ok := args["command"].(string); ok {
			return c
		}
	}
	parts := []string{tool}
	for _, k := range slices.Sorted(maps.Keys(args)) {
		if args[k] == nil {
			continue
		}
		parts = append(parts, k+"="+value(args[k]))
	}
	return strings.Join(parts, " ")
}

// value prints a string bare when nothing in it could be misread, and
// anything else as JSON, whose strings are quoted and escaped.
func value(v any) string {
	if s, ok := v.(string); ok {
		if plain(s) {
			return s
		}
		return strconv.Quote(s)
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return strconv.Quote(fmt.Sprint(v))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func plain(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) || unicode.IsSpace(r) || strings.ContainsRune(`"'\=`, r) {
			return false
		}
	}
	return true
}
