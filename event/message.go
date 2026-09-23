package event

// The transcript's vocabulary. Here rather than in the model client
// for the reason Usage is: a fact carries it, and this package may
// import neither side.

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

// CallIDs are what a Step's results must answer, in order.
func CallIDs(calls []ToolCall) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.ID
	}
	return out
}
