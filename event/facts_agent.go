package event

import "github.com/google/uuid"

// An agent is one model with a transcript of its own: the root, which
// answers the human, or a child a spawn_agent tool call started.

// AgentStarted says a child exists, queued until it has a slot. ToolCall is
// the parent's spawn call, never one of the child's own.
type AgentStarted struct {
	fact
	Agent    uuid.UUID `json:"Agent"`
	ToolCall uuid.UUID `json:"ToolCall"`
	Name     string    `json:"Name"`
	Task     string    `json:"Task"`
}

func (AgentStarted) Kind() Kind { return AgentStartedKind }

// AgentEnded says a child stopped and why. Its report is the spawn call's
// ToolCallEnded, so it is not repeated here.
type AgentEnded struct {
	fact
	Agent  uuid.UUID   `json:"Agent"`
	Reason AgentReason `json:"Reason"`
	// Why says what cut it short, such as "stopped at 30 steps", empty when done.
	Why   string `json:"Why"`
	Usage Usage  `json:"Usage"`
}

func (AgentEnded) Kind() Kind { return AgentEndedKind }

// AgentReason is how a child stopped.
type AgentReason string

const (
	AgentDone    AgentReason = "done"    // it answered
	AgentPartial AgentReason = "partial" // a limit stopped it, and it reported what it had
	AgentStopped AgentReason = "stopped" // the human stopped it
	AgentFailed  AgentReason = "failed"  // its model call failed
	AgentAborted AgentReason = "aborted" // the Turn was aborted, or the process exited
)
