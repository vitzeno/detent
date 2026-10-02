// Package model talks to an OpenAI-compatible endpoint that calls
// tools. One Complete is one Step: the transcript in, prose and zero
// or more tool calls out.
package model

import (
	"strings"

	"github.com/vitzeno/detent/event"
)

// Reply is one Step's result. No Calls means the model stopped asking
// for tools, which ends the Turn unless the reply is Unfinished.
type Reply struct {
	Text  string
	Calls []event.ToolCall
	// Stop is the endpoint's own reason the reply ended, "length" when cut off.
	Stop string
	// Thinking: Text is the model's reasoning, since it gave no answer.
	Thinking bool
}

// Unfinished is a reply with neither a call nor an answer: cut off at the
// output limit, empty, or reasoning that stopped before saying anything.
func (r Reply) Unfinished() bool {
	return len(r.Calls) == 0 && (r.Stop == "length" || r.Thinking || strings.TrimSpace(r.Text) == "")
}
