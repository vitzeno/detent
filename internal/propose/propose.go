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
	// File is the path Command shows or writes the complete content of —
	// cats it, redirects into it, writes a heredoc to it — self-reported
	// by the model. "" means Command doesn't center on one identifiable
	// file (it reads/writes several, or only part of one). The harness
	// reads the path from disk after Command runs (never trusts Command's
	// own stdout for this, since a write often prints nothing at all) and
	// offers it in the editor component, open for editing regardless of
	// whether Command only read it; the model never sees or produces file
	// content beyond what it already wrote in Command itself.
	File string
}
