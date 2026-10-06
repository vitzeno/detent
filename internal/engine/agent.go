package engine

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/tool"
)

// agent is one model working through Steps over a transcript of its own.
// The root answers the human.
type agent struct {
	// id is uuid.Nil for the root.
	id    uuid.UUID
	name  string
	model Completer
	tools *tool.Registry
	// repeat is the agent's own, so one agent's reruns never refuse another's.
	repeat    *repeatHook
	assessors []Assessor

	// review is what a reviewer comments on, nil for every other agent.
	review *reviewRun
	// limits bound a child, zero for the root, which asks to pass its bound instead.
	limits childLimits

	// mu guards tr, which the agent's goroutine writes and a caller may read.
	mu sync.Mutex
	tr transcript
}

// newAgent builds the hook chain around the agent's own repeat check,
// cheapest first, with extra (a caller's hooks, then Jev) last.
func newAgent(id uuid.UUID, name string, m Completer, tools *tool.Registry, extra ...Assessor) *agent {
	a := &agent{id: id, name: name, model: m, tools: tools, repeat: newRepeatHook(defaultRepeatLimit)}
	a.assessors = append([]Assessor{toolFloor{}, mcpFloor{}, regexHook{}, a.repeat}, extra...)
	return a
}

// childLimits are how far a child may go before it must report: its Steps,
// its transcript in tokens, and its time once it has a slot.
type childLimits struct {
	steps, context int
	timeout        time.Duration
}

// root is whether this agent answers the human. Only the root hears notes,
// asks to pass its bound, checks its work, compacts and is measured.
func (a *agent) root() bool { return a.id == uuid.Nil }

// lock runs fn holding the transcript lock. Never used around anything
// blocking: a model call takes seconds.
func (a *agent) lock(fn func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fn()
}

// messages copies the log, since the agent's goroutine owns the original.
func (a *agent) messages() []event.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]event.Message(nil), a.tr.messages()...)
}
