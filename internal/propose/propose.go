// Package propose proposes the next shell command from a session transcript.
package propose

import "context"

// Role is a chat message role.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one turn in the session transcript.
type Message struct {
	Role    Role
	Content string
}

// Proposal is one turn's output: the next command, or a done signal.
type Proposal struct {
	Command   string
	Rationale string // one or two sentences, shown next to the command
	Done      bool
	Summary   string // shown once, when this goal finishes
}

// Proposer proposes the next step for the currently open goal.
type Proposer interface {
	Propose(ctx context.Context, messages []Message) (Proposal, error)
}
