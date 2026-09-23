// Package model talks to an OpenAI-compatible endpoint that calls
// tools. One Complete is one Step: the transcript in, prose and zero
// or more tool calls out.
package model

// Reply is one Step's result. No Calls means the model stopped asking
// for tools, which ends the Turn.
type Reply struct {
	Text  string
	Calls []ToolCall
	// Stop is the endpoint's own reason, kept for logging.
	Stop string
}

// ToolCall is one thing the model wants run. ID is what the answering
// message must carry back.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
	// Err is set when the model's arguments were not valid JSON. The
	// call still needs a result, or the next Step is malformed.
	Err string
}

// Message is one transcript entry. An assistant message with Calls and
// the tool messages answering it are one Step, and indivisible.
type Message struct {
	Role    Role
	Content string
	// Calls belong to an assistant message.
	Calls []ToolCall
	// CallID answers one call; set on RoleTool alone.
	CallID string
}

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Answer builds the tool message that closes one call.
func Answer(call ToolCall, content string) Message {
	return Message{Role: RoleTool, CallID: call.ID, Content: content}
}

// IDs are what a Step's results must answer, in order.
func IDs(calls []ToolCall) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.ID
	}
	return out
}
