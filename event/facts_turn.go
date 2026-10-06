package event

import "github.com/google/uuid"

// A Turn is one prompt and everything the agent did about it.

// TurnStarted opens a Turn.
type TurnStarted struct {
	fact
	Turn   uuid.UUID `json:"Turn"`
	N      int       `json:"N"` // 1-based, what the human sees and /rollback takes
	Prompt string    `json:"Prompt"`
	// Review names the review a reviewer agent works for. Such a Turn is no
	// request: it has no N, writes nothing to the root's transcript and is never undone.
	Review uuid.UUID `json:"Review"`
	Files  int       `json:"Files"` // how many files a review's diff holds
}

func (TurnStarted) Kind() Kind { return TurnStartedKind }

// CheckpointTaken is the Turn's one snapshot, and all a rollback restores.
type CheckpointTaken struct {
	fact
	Turn     uuid.UUID `json:"Turn"`
	Snapshot string    `json:"Snapshot"` // container, "" when unsandboxed
	Tree     string    `json:"Tree"`     // the human's own working directory
}

func (CheckpointTaken) Kind() Kind { return CheckpointTakenKind }

// TurnEnded closes a Turn, however it stopped.
type TurnEnded struct {
	fact
	Turn    uuid.UUID `json:"Turn"`
	Reason  EndReason `json:"Reason"`
	Summary string    `json:"Summary"`
	Usage   Usage     `json:"Usage"` // the whole Turn's cost
	Tree    string    `json:"Tree"`  // the human's files as it left them, "" if not checkpointed
}

func (TurnEnded) Kind() Kind { return TurnEndedKind }

// EndReason is how a Turn stopped. No Declined: that stops a tool call.
type EndReason string

const (
	EndDone    EndReason = "done"    // the model stopped asking for tools
	EndStopped EndReason = "stopped" // the judge ended it, in sessions from before it only advised
	EndAborted EndReason = "aborted" // the human said stop
	EndBound   EndReason = "bound"   // the human declined to continue
	EndError   EndReason = "error"
)

// BoundReached pauses the Turn at MaxSteps to ask. Not an ending.
type BoundReached struct {
	fact
	Turn      uuid.UUID `json:"Turn"`
	Steps     int       `json:"Steps"`
	ToolCalls int       `json:"ToolCalls"`
}

func (BoundReached) Kind() Kind { return BoundReachedKind }

// RolledBack says a Turn's checkpoint was restored.
type RolledBack struct {
	fact
	Turn        uuid.UUID `json:"Turn"`
	RevertFiles bool      `json:"RevertFiles"`
}

func (RolledBack) Kind() Kind { return RolledBackKind }
