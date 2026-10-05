package engine

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/model"
)

// A subagent is a spawn_agent tool call the engine runs itself: a child agent
// with a transcript of its own, whose report is the call's result. Nothing
// else of it reaches the parent, and the bus only observes it.

const (
	// DefaultMaxAgents is how many subagents one Turn may start.
	DefaultMaxAgents = 10
	// DefaultChildContextTokens is how far a child's transcript may grow
	// before it must report. Children never compact, since they are short.
	DefaultChildContextTokens = 64_000
	// DefaultChildTimeout is how long a child may run once it has a slot.
	DefaultChildTimeout = 10 * time.Minute

	// childSteps is how many Steps a child takes before it must report.
	childSteps = 30
	// childSlots is how many children run at once. The rest wait, queued.
	childSlots = 4
	// reportTimeout bounds the call asking a child cut short for its report.
	reportTimeout = 2 * time.Minute
)

// childTools is what a subagent may call: reading and searching, never
// writing, never spawning, and no MCP tool holding the human's credentials.
var childTools = []string{"read_file", "grep", "find_files", "list_dir", "web_search", "skill"}

// What cut a child short, read back off its ctx.
var (
	errStopped  = errors.New("the human stopped this subagent")
	errTimedOut = errors.New("its time ran out")
)

// spawn runs a child to its end and answers the spawn call with its report.
func (e *Engine) spawn(ctx context.Context, t *turnState, parent *agent, p *toolCallPlan) {
	if e.childModel == nil {
		p.finish("Not run: subagents are not available in this session.")
		return
	}
	n := int(t.spawned.Add(1))
	if n > e.maxAgents {
		p.finish(fmt.Sprintf("Not run: a request may start at most %d subagents. Carry on with what they reported.", e.maxAgents))
		return
	}
	task := p.prepared.Args.String("task")
	name := cmp.Or(strings.TrimSpace(p.prepared.Args.String("name")), fmt.Sprintf("agent-%d", n))
	child := newAgent(uuid.Must(uuid.NewV7()), name, e.childModel, parent.tools.Only(childTools...), e.extra...)

	p.ended = true
	start := time.Now()
	e.bus.Publish(event.ToolCallStarted{ToolCall: p.id, Runner: "agent"})
	// Published before it has a slot, so a queued child can be seen and stopped.
	e.bus.Publish(event.AgentStarted{Agent: child.id, ToolCall: p.id, Name: name, Task: task})

	cctx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	e.track(child.id, stop)
	defer e.untrack(child.id)

	end := e.runChild(cctx, t, child, task)
	reason, report := e.report(ctx, cctx, t, child, &end)
	report = capture.Clip(report, capture.MaxResultBytes)

	e.bus.Publish(event.AgentEnded{Agent: child.id, Reason: reason, Usage: end.used})
	out := event.Result{Stdout: report}
	if reason == event.AgentAborted {
		out = event.Result{Err: "the request was aborted"}
	}
	e.bus.Publish(event.ToolCallEnded{ToolCall: p.id, Result: out, Took: time.Since(start)})
	p.finish(report)
}

// runChild waits for a slot, then runs the child's Steps until it answers or
// something stops it. Its time limit starts once it runs, not while it waits.
func (e *Engine) runChild(ctx context.Context, t *turnState, child *agent, task string) agentEnd {
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	case <-ctx.Done():
		return agentEnd{reason: event.EndAborted}
	}
	ctx, cancel := context.WithTimeoutCause(ctx, e.childTimeout, errTimedOut)
	defer cancel()
	e.appended(child, t.id, uuid.Nil, func() []event.Message { return child.tr.user(1, task) })
	end := e.runAgent(ctx, t, child)
	// Read here: the time limit is this ctx's, and the spawn's never sees it.
	if errors.Is(context.Cause(ctx), errTimedOut) {
		end.cut = "ran for " + model.Brief(e.childTimeout)
	}
	return end
}

// report is how a child ended and what the parent reads. One cut short is
// asked once more, with no tools, for what it found: the last Step of a
// child still working rarely says anything useful.
func (e *Engine) report(ctx, cctx context.Context, t *turnState, child *agent, end *agentEnd) (event.AgentReason, string) {
	if end.reason == event.EndDone {
		return event.AgentDone, end.text
	}
	if t.aborted.Load() || ctx.Err() != nil {
		return event.AgentAborted, "This subagent was not finished: the request was aborted."
	}
	if end.steps == 0 && errors.Is(context.Cause(cctx), errStopped) {
		return event.AgentStopped, "[the human stopped this subagent before it started] Do not start it again with the same task."
	}
	reason, why := event.AgentPartial, end.cut
	switch {
	case errors.Is(context.Cause(cctx), errStopped):
		reason, why = event.AgentStopped, "the human stopped this subagent"
	case end.reason == event.EndError:
		reason, why = event.AgentFailed, "it failed: "+end.text
	}
	prefix := "[partial: " + why + "]\n"
	if reason == event.AgentStopped {
		prefix = "[the human stopped this subagent] Do not start it again with the same task.\n"
	}
	return reason, prefix + e.lastWords(ctx, t, child, why, end)
}

// lastWords asks a child that was cut short for its report, on the parent's
// ctx since its own is done. Failing that, its last words stand.
func (e *Engine) lastWords(ctx context.Context, t *turnState, child *agent, why string, end *agentEnd) string {
	note := "[you have to stop now: " + why + "] Reply with your report of what you found so far, " +
		"with file paths and line numbers, and what you did not get to."
	e.appended(child, t.id, uuid.Nil, func() []event.Message { return child.tr.note(note) })

	rctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()
	step := uuid.Must(uuid.NewV7())
	e.bus.Publish(event.StepStarted{Turn: t.id, Step: step, N: end.steps + 1, Agent: child.id})
	reply, used, err := child.model.Complete(rctx, child.messages(), nil)
	t.addUsage(used)
	end.used = end.used.Add(used)
	e.bus.Publish(event.StepEnded{Turn: t.id, Step: step, Usage: used, Stop: reply.Stop, Agent: child.id})
	if err == nil && strings.TrimSpace(reply.Text) != "" {
		e.bus.Publish(event.ModelText{Turn: t.id, Step: step, Text: reply.Text, Agent: child.id})
		e.appended(child, t.id, step, func() []event.Message { return child.tr.say(reply.Text) })
		return reply.Text
	}
	if text := lastSaid(child.messages()); text != "" {
		return text
	}
	return "It found nothing it could report."
}

// over is whether a child's transcript has passed its context budget.
func (a *agent) over(tokens int) bool {
	var n int
	a.lock(func() { n = a.tr.bytes() })
	return n > tokens*bytesPerToken
}

// lastSaid is the last non-empty thing an agent wrote.
func lastSaid(msgs []event.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if m := msgs[i]; m.Role == event.RoleAssistant && strings.TrimSpace(m.Content) != "" {
			return m.Content
		}
	}
	return ""
}

// track keeps a running child's stop for StopAgent, and untrack forgets it.
func (e *Engine) track(id uuid.UUID, stop context.CancelCauseFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.agents[id] = stop
}

func (e *Engine) untrack(id uuid.UUID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.agents, id)
}

// stopAgent ends one child, which still reports. One already ended is gone.
func (e *Engine) stopAgent(id uuid.UUID) {
	e.mu.Lock()
	stop := e.agents[id]
	e.mu.Unlock()
	if stop != nil {
		stop(errStopped)
	}
}
