// Package model talks to an OpenAI-compatible endpoint that calls
// tools. One Complete is one Step: the transcript in, prose and zero
// or more tool calls out.
package model

import "github.com/vitzeno/detent/event"

// Reply is one Step's result. No Calls means the model stopped asking
// for tools, which ends the Turn.
type Reply struct {
	Text  string
	Calls []event.ToolCall
	// Stop is the endpoint's own reason, kept for logging.
	Stop string
}
