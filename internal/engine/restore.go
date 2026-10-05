package engine

import (
	"slices"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// cutOffNote tells the model why its last request has no answer.
const cutOffNote = "[detent exited before your last request finished] " +
	"Anything it was running did not finish. Check before relying on it."

// Restore rebuilds a session from its stored facts, undo and reset included,
// never compacting. Call it before Run, or it races the Turn goroutine.
func (e *Engine) Restore(records []event.Record) {
	e.resumed = len(records)
	e.leftOpen = leftOpenBy(records)
	type begun struct{ n, mark int }
	turns := map[uuid.UUID]begun{}
	e.root.lock(func() {
		for _, r := range records {
			switch v := r.Event.(type) {
			case event.Appended:
				// A child's transcript never entered the root's, so it stays out.
				if v.Agent == uuid.Nil {
					e.root.tr.msgs = append(e.root.tr.msgs, v.Messages...)
				}
			case event.TurnStarted:
				turns[v.Turn] = begun{n: v.N, mark: e.root.tr.mark()}
				e.turns = v.N
				e.root.tr.begin(v.N, v.Prompt)
			case event.RolledBack:
				if b, ok := turns[v.Turn]; ok && e.root.tr.truncate(b.mark) {
					e.turns = b.n - 1
				}
			case event.SessionReset:
				e.root.tr.reset()
				e.turns = 0
			case event.SessionStarted:
				e.session = v.Session
			}
		}
	})
}

// Resumable reports the last ordinal a session reached, which is what
// the bus has to continue from.
func Resumable(records []event.Record) uint64 {
	var last uint64
	for _, r := range records {
		last = max(last, r.Ordinal)
	}
	return last
}

// openWork is what the old process started and never finished. It died with
// them, so Run ends each one as a fact a later replay will see.
type openWork struct {
	turns    []uuid.UUID
	calls    []uuid.UUID
	commands []uuid.UUID
	agents   []uuid.UUID
}

// endLeftOpen ends what the old process left open, then tells the model.
func (e *Engine) endLeftOpen(o openWork) {
	if o.empty() {
		return
	}
	cut := event.Result{Err: "detent exited before this finished"}
	for _, id := range o.calls {
		e.bus.Publish(event.ToolCallEnded{ToolCall: id, Result: cut})
	}
	for _, id := range o.commands {
		e.bus.Publish(event.UserCommandEnded{UserCommand: id, Result: cut})
	}
	for _, id := range o.agents {
		e.bus.Publish(event.AgentEnded{Agent: id, Reason: event.AgentAborted})
	}
	for _, id := range o.turns {
		e.bus.Publish(event.TurnEnded{Turn: id, Reason: event.EndError,
			Summary: "detent exited before this request finished"})
	}
	if len(o.turns) > 0 {
		e.appended(e.root, uuid.Nil, uuid.Nil, func() []event.Message { return e.root.tr.note(cutOffNote) })
	}
}

// leftOpenBy reads records for what never ended. A Turn undone or reset away
// is gone, and so are its tool calls and the agents they spawned.
func leftOpenBy(records []event.Record) openWork {
	var turns, calls, commands, agents []uuid.UUID
	stepTurn := map[uuid.UUID]uuid.UUID{}
	callTurn := map[uuid.UUID]uuid.UUID{}
	// spawnedBy is each agent's spawn call, which places it in a Turn.
	spawnedBy := map[uuid.UUID]uuid.UUID{}
	drop := func(list []uuid.UUID, id uuid.UUID) []uuid.UUID {
		return slices.DeleteFunc(list, func(x uuid.UUID) bool { return x == id })
	}
	for _, r := range records {
		switch v := r.Event.(type) {
		case event.TurnStarted:
			turns = append(turns, v.Turn)
		case event.TurnEnded:
			turns = drop(turns, v.Turn)
		case event.StepStarted:
			stepTurn[v.Step] = v.Turn
		case event.ToolCallProposed:
			calls = append(calls, v.ToolCall)
			callTurn[v.ToolCall] = stepTurn[v.Step]
		case event.ToolCallEnded:
			calls = drop(calls, v.ToolCall)
		case event.AgentStarted:
			agents = append(agents, v.Agent)
			spawnedBy[v.Agent] = v.ToolCall
		case event.AgentEnded:
			agents = drop(agents, v.Agent)
		case event.UserCommandStarted:
			commands = append(commands, v.UserCommand)
		case event.UserCommandEnded:
			commands = drop(commands, v.UserCommand)
		case event.RolledBack:
			// Undo takes back the Turn and every one after it.
			if i := slices.Index(turns, v.Turn); i >= 0 {
				turns = turns[:i]
			}
		case event.SessionReset:
			turns, calls, agents = nil, nil, nil
		}
	}
	// A call in a Turn no longer open was finished or taken back, and an agent with it.
	calls = slices.DeleteFunc(calls, func(c uuid.UUID) bool { return !slices.Contains(turns, callTurn[c]) })
	agents = slices.DeleteFunc(agents, func(a uuid.UUID) bool {
		return !slices.Contains(turns, callTurn[spawnedBy[a]])
	})
	return openWork{turns: turns, calls: calls, commands: commands, agents: agents}
}

func (o openWork) empty() bool {
	return len(o.turns)+len(o.calls)+len(o.commands)+len(o.agents) == 0
}
