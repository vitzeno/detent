// Package propose proposes the next shell command from a session transcript.
package propose

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
	// File is the path Command shows or writes in full (cat, redirect,
	// heredoc), self-reported by the model. "" when Command touches
	// several files or only part of one. The harness reads it from disk
	// after Command runs, never from stdout — a write often prints nothing.
	File string
}
