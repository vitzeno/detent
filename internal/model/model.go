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
	// Stop is the endpoint's own reason the reply ended: "length" when cut
	// off, "error" when the provider failed mid-reply.
	Stop string
	// Thinking: Text is the model's reasoning, since it gave no answer.
	Thinking bool
}

// Unfinished is a reply with no call that did not end the way an answer does:
// stopped for any reason but a finish, empty, or reasoning with nothing after it.
func (r Reply) Unfinished() bool {
	return len(r.Calls) == 0 && (!finished[r.Stop] || r.Thinking || strings.TrimSpace(r.Text) == "")
}

// finished are the stop reasons an answer ends with. An endpoint that sends
// none is taken at its word, and anything else, "error" included, is not an answer.
var finished = map[string]bool{"": true, "stop": true, "end_turn": true}
