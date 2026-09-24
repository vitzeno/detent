package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// runStep answers every Call however it went, keyed by the model's own
// id because that is what the transcript pairs on.
func (e *Engine) runStep(ctx context.Context, t *turnState, step uuid.UUID, reply model.Reply) map[string]string {
	plans := e.plan(step, reply)

	var serial []*callPlan
	var parallel []*callPlan
	for _, p := range plans {
		switch {
		case p.done:
		case p.risk.ReadOnly() && !p.risk.Dangerous:
			parallel = append(parallel, p)
		default:
			serial = append(serial, p)
		}
	}

	e.runParallel(ctx, parallel)
	for _, p := range serial {
		if t.aborted || ctx.Err() != nil {
			break
		}
		if p.risk.Dangerous && !e.approve(ctx, t, p) {
			p.finish("The human declined this call. Do not repeat it; try something else.")
			continue
		}
		e.execute(ctx, p)
	}

	out := make(map[string]string, len(plans))
	for _, p := range plans {
		if !p.done {
			p.finish("This call was not run: the request was aborted.")
		}
		out[p.call.ID] = p.answer
	}
	return out
}

// callPlan is one Call's journey. answer is always set by the end,
// which is what keeps the transcript well formed.
type callPlan struct {
	id     uuid.UUID
	call   event.ToolCall
	cmd    string
	risk   event.Risk
	answer string
	done   bool
	// prepared is what the registry made of the call, and says which
	// executor runs it.
	prepared tool.Call
}

func (p *callPlan) finish(answer string) { p.answer, p.done = answer, true }

// plan validates and assesses, settling anything that cannot run.
// Every failure here is an answer the model reads, never a Go error.
func (e *Engine) plan(step uuid.UUID, reply model.Reply) []*callPlan {
	out := make([]*callPlan, 0, len(reply.Calls))
	for i, c := range reply.Calls {
		p := &callPlan{id: uuid.Must(uuid.NewV7()), call: c}
		out = append(out, p)
		e.bus.Publish(event.CallProposed{Call: p.id, Step: step, Tool: c.Name, Args: c.Args,
			Renders: e.renders(c.Name)})

		switch {
		case c.Err != "":
			p.finish(c.Err + ". Send the arguments as valid JSON matching the schema.")
			continue
		case i >= e.maxCalls:
			p.finish(fmt.Sprintf("Not run: a step may ask for at most %d calls. Ask again in the next step.", e.maxCalls))
			continue
		}

		prepared, err := e.tools.Prepare(c.Name, c.Args)
		if err != nil {
			p.finish(err.Error())
			continue
		}
		p.prepared, p.cmd = prepared, prepared.Command
		p.risk = e.assess(context.Background(), prepared)
		if n := e.repeat.count(prepared.Command); n > DefaultRepeatLimit {
			p.finish(fmt.Sprintf("Not run: this exact command has already run %d times and printed the same thing. Try something else.", n))
			continue
		}
		e.bus.Publish(event.CallAssessed{Call: p.id, Risk: p.risk})
	}
	return out
}

// runParallel runs the read-only Calls together, capped. They change
// nothing, so nothing depends on their order.
func (e *Engine) runParallel(ctx context.Context, plans []*callPlan) {
	if len(plans) == 0 {
		return
	}
	sem := make(chan struct{}, max(1, e.parallel))
	var wg sync.WaitGroup
	for _, p := range plans {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			e.execute(ctx, p)
		}()
	}
	wg.Wait()
}

// approve publishes the question and waits. Whoever answers is nobody
// the engine knows about.
func (e *Engine) approve(ctx context.Context, t *turnState, p *callPlan) bool {
	e.bus.Publish(event.ApprovalAsked{
		Call: p.id, Tool: p.call.Name, Args: p.call.Args,
		Rationale: describe(p.risk), Risk: p.risk,
	})
	for {
		select {
		case <-ctx.Done():
			return false
		case ev := <-t.inbox:
			if r, ok := ev.(event.ResolveApproval); ok && r.Call == p.id {
				return r.Approved
			}
			t.absorb(ev)
			if t.aborted {
				return false
			}
		}
	}
}

func (e *Engine) execute(ctx context.Context, p *callPlan) {
	if p.prepared.Executor != "" {
		e.invoke(ctx, p)
		return
	}
	runner, mode := e.runners.Select(p.risk)
	if runner == nil {
		p.finish("No runner is wired; nothing could be executed.")
		return
	}
	e.bus.Publish(event.CallStarted{Call: p.id, Runner: mode})

	lines := make(chan capture.StreamEvent, 64)
	relayed := make(chan struct{})
	go func() {
		defer close(relayed)
		for l := range lines {
			e.bus.Publish(event.OutputChunk{Call: p.id, Line: l.Line, Stderr: l.Stderr})
		}
	}()

	start := time.Now()
	res, err := runSafely(ctx, runner, p.cmd, lines)
	took := time.Since(start)
	// A Runner closes the channel when output ends. One that forgets
	// would wedge the Turn, which is worse than a leaked goroutine.
	select {
	case <-relayed:
	case <-time.After(relayGrace):
	}

	out := event.Result{
		ExitCode: res.ExitCode, Stdout: res.Stdout, Stderr: res.Stderr, Truncated: res.Truncated,
	}
	if err != nil {
		out.Err = err.Error()
	}
	e.bus.Publish(event.CallEnded{Call: p.id, Result: out, Took: took})
	p.finish(formatResult(p.cmd, out))
}

// relayGrace is how long to wait on a Runner that did not close its
// output channel before giving up on it.
const relayGrace = 2 * time.Second

// runSafely turns a panicking Runner into a failed Call. Taking the
// session down would lose every checkpoint still rollable.
func runSafely(ctx context.Context, r Runner, cmd string, lines chan capture.StreamEvent) (res capture.Result, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("runner panicked: %v", v)
			close(lines)
		}
	}()
	return r.Run(ctx, cmd, lines)
}

// formatResult is what the model reads back.
func formatResult(cmd string, r event.Result) string {
	if r.Err != "" {
		return fmt.Sprintf("Could not run `%s`: %s", cmd, r.Err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Exit code %d.", r.ExitCode)
	if r.Stdout != "" {
		fmt.Fprintf(&b, "\nstdout:\n%s", r.Stdout)
	}
	if r.Stderr != "" {
		fmt.Fprintf(&b, "\nstderr:\n%s", r.Stderr)
	}
	if r.Stdout == "" && r.Stderr == "" {
		b.WriteString(" No output.")
	}
	if r.Truncated {
		b.WriteString("\n[output truncated at capture]")
	}
	return b.String()
}

// renders asks the tool how its output should be read, so a front-end
// does not have to guess at a shape the tool already knows
// this is advisory, the front-end may choose to respect it or not
func (e *Engine) renders(name string) string {
	t, ok := e.tools.Lookup(name)
	if !ok {
		return ""
	}
	return t.Describe().Renders
}

// invoke runs a Call with no command. Runner names the executor, so
// the row says where it ran rather than implying the sandbox.
func (e *Engine) invoke(ctx context.Context, p *callPlan) {
	if e.invoker == nil {
		p.finish("No invoker is wired for " + p.prepared.Executor + "; nothing could be executed.")
		return
	}
	e.bus.Publish(event.CallStarted{Call: p.id, Runner: p.prepared.Executor})

	start := time.Now()
	res, err := invokeSafely(ctx, e.invoker, p.prepared)
	out := event.Result{
		ExitCode: res.ExitCode, Stdout: res.Stdout, Stderr: res.Stderr, Truncated: res.Truncated,
	}
	if err != nil {
		out.Err = err.Error()
	}
	e.bus.Publish(event.CallEnded{Call: p.id, Result: out, Took: time.Since(start)})
	p.finish(formatResult(p.cmd, out))
}

// invokeSafely turns a panicking Invoker into a failed Call, the way
// runSafely does for a Runner.
func invokeSafely(ctx context.Context, in Invoker, c tool.Call) (res capture.Result, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("invoker panicked: %v", v)
		}
	}()
	return in.Invoke(ctx, c), nil
}
