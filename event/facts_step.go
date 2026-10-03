package event

import "github.com/google/uuid"

// A Step is one model round trip, and the transcript's atom.

// StepStarted opens a Step.
type StepStarted struct {
	fact
	Turn uuid.UUID `json:"Turn"`
	Step uuid.UUID `json:"Step"`
	N    int       `json:"N"`
}

func (StepStarted) Kind() Kind { return StepStartedKind }

// StepEnded closes a Step with what it cost.
type StepEnded struct {
	fact
	Turn      uuid.UUID `json:"Turn"`
	Step      uuid.UUID `json:"Step"`
	ToolCalls int       `json:"ToolCalls"` // how many the model asked for, 0 when it stopped
	Stop      string    `json:"Stop"`      // the endpoint's reason the reply ended
	Usage     Usage     `json:"Usage"`
}

func (StepEnded) Kind() Kind { return StepEndedKind }

// ModelText is the model's prose: its own row, judged like output.
type ModelText struct {
	fact
	Turn uuid.UUID `json:"Turn"`
	Step uuid.UUID `json:"Step"`
	Text string    `json:"Text"`
}

func (ModelText) Kind() Kind { return ModelTextKind }
