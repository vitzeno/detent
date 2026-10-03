package event

import (
	"time"

	"github.com/google/uuid"
)

// A user command is one command the human ran themselves. Not a tool call: the
// model never asked for it, so nothing assesses or approves it.

// UserCommandStarted says a user command began running.
type UserCommandStarted struct {
	fact
	UserCommand uuid.UUID `json:"UserCommand"`
	Command     string    `json:"Command"`
	Runner      string    `json:"Runner"` // host or sandbox
}

func (UserCommandStarted) Kind() Kind { return UserCommandStartedKind }

// UserCommandEnded is a user command's whole result.
type UserCommandEnded struct {
	fact
	UserCommand uuid.UUID     `json:"UserCommand"`
	Result      Result        `json:"Result"`
	Took        time.Duration `json:"Took"`
}

func (UserCommandEnded) Kind() Kind { return UserCommandEndedKind }
