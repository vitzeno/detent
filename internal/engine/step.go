package engine

import (
	"context"
	"errors"
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

// callPlan is one Call's journey. answer is always set by the end,
// which is what keeps the transcript well formed.
type callPlan struct {
	id     uuid.UUID
	call   event.ToolCall
	cmd    string
	risk   event.Risk
	answer string
	done   bool
	// ended is set once CallEnded is out, so every proposed row finishes.
	ended bool
	// prepared is the registry's lowering, and says which executor runs it.
	prepared tool.Call
}

func (p *callPlan) finish(answer string) { p.answer, p.done = answer, true }

// readOnly is what the tool declares and nothing widened. A hook can
// lower unknown to read-only, so the verdict alone cannot decide this.
func (p *callPlan) readOnly() bool {
	return p.prepared.Mutability == event.MutRead && p.risk.ReadOnly()
}

// approval is how a confirm ended: an abort is not the human saying no.
type approval int

const (
	declined approval = iota
	approved
	abandoned
)

// runStep answers every Call however it went, in the order asked.
// Contiguous read-only Calls run together, never ahead of an earlier write.
func (e *Engine) runStep(ctx context.Context, t *turnState, step uuid.UUID, reply model.Reply) []string {
	plans := e.plan(ctx, step, reply)
	stopping := func() bool { return t.aborted.Load() || ctx.Err() != nil }

	var batch []*callPlan
	flush := func() {
		if len(batch) > 0 && !stopping() {
			e.runParallel(ctx, batch)
		}
		batch = nil
	}
serial:
	for _, p := range plans {
		switch {
		case p.done:
			continue
		case p.readOnly() && !p.risk.Dangerous:
			batch = append(batch, p)
			continue
		}
		flush()
		if stopping() {
			break
		}
		if p.risk.Dangerous {
			switch e.approve(ctx, t, p) {
			case declined:
				p.finish("The human declined this call. Do not repeat it; try something else.")
				continue
			case abandoned:
				break serial
			}
		}
		e.execute(ctx, p)
		if !p.readOnly() {
			t.changed = true
		}
	}
	flush()

	out := make([]string, len(plans))
	for i, p := range plans {
		if !p.done {
			p.finish("This call was not run: the request was aborted.")
		}
		if !p.ended {
			e.bus.Publish(event.CallEnded{Call: p.id, Result: event.Result{Err: p.answer}})
		}
		out[i] = p.answer
	}
	return out
}

// plan validates and assesses, settling anything that cannot run.
// Every failure here is an answer the model reads, never a Go error.
func (e *Engine) plan(ctx context.Context, step uuid.UUID, reply model.Reply) []*callPlan {
	out := make([]*callPlan, 0, len(reply.Calls))
	ids := make(map[string]bool, len(reply.Calls))
	for i, c := range reply.Calls {
		p := &callPlan{id: uuid.Must(uuid.NewV7()), call: c}
		out = append(out, p)
		e.bus.Publish(event.CallProposed{Call: p.id, Step: step, Tool: c.Name, Args: c.Args,
			Renders: e.renders(c.Name), Executor: e.executor(c.Name)})

		dup := ids[c.ID]
		ids[c.ID] = true
		switch {
		case c.Err != "":
			p.finish(c.Err + ". Send the arguments as valid JSON matching the schema.")
			continue
		case dup:
			p.finish(fmt.Sprintf("Not run: another call in this step has the id %q. Give each call its own id.", c.ID))
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
		if n, refused := e.repeat.refuses(prepared.Command); refused {
			p.finish(fmt.Sprintf("Not run: this exact command has already run %d times in this request and printed the same thing each time. Try something else.", n))
			continue
		}
		p.risk = e.assess(ctx, prepared)
		e.bus.Publish(event.CallAssessed{Call: p.id, Risk: p.risk})
	}
	return out
}

// runParallel runs read-only Calls together. They change nothing, so
// nothing depends on their order.
func (e *Engine) runParallel(ctx context.Context, plans []*callPlan) {
	var wg sync.WaitGroup
	for _, p := range plans {
		wg.Go(func() { e.execute(ctx, p) })
	}
	wg.Wait()
}

// approve publishes the question and waits. Whoever answers is nobody
// the engine knows about.
func (e *Engine) approve(ctx context.Context, t *turnState, p *callPlan) approval {
	e.bus.Publish(event.ApprovalAsked{
		Call: p.id, Tool: p.call.Name, Args: p.call.Args,
		Rationale: describe(p.risk), Risk: p.risk,
	})
	for {
		select {
		case <-ctx.Done():
			return abandoned
		case ev := <-t.inbox:
			if r, ok := ev.(event.ResolveApproval); ok && r.Call == p.id {
				if r.Approved {
					return approved
				}
				return declined
			}
			t.absorb(ev)
			if t.aborted.Load() {
				return abandoned
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
	p.ended = true
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
	cctx, cancel := context.WithTimeout(ctx, e.commandTimeout)
	defer cancel()
	res, err := runSafely(cctx, runner, p.cmd, lines)
	took := time.Since(start)
	close(lines)
	<-relayed

	out := event.Result{
		ExitCode: res.ExitCode, Stdout: res.Stdout, Stderr: res.Stderr, Truncated: res.Truncated,
	}
	if err != nil {
		out.Err = e.why(ctx, cctx, err)
	}
	e.bus.Publish(event.CallEnded{Call: p.id, Result: out, Took: took})
	p.finish(formatResult(p.cmd, out))
	e.repeat.ran(p.cmd, p.answer)
}

// invoke runs a Call with no command. Runner names the executor, so
// the row says where it ran rather than implying the sandbox.
func (e *Engine) invoke(ctx context.Context, p *callPlan) {
	if e.invoker == nil {
		p.finish("No invoker is wired for " + p.prepared.Executor + "; nothing could be executed.")
		return
	}
	p.ended = true
	e.bus.Publish(event.CallStarted{Call: p.id, Runner: p.prepared.Executor})

	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, e.commandTimeout)
	defer cancel()
	res, err := invokeSafely(cctx, e.invoker, p.prepared)
	out := event.Result{
		ExitCode: res.ExitCode, Stdout: res.Stdout, Stderr: res.Stderr, Truncated: res.Truncated,
	}
	if err != nil {
		out.Err = e.why(ctx, cctx, err)
	}
	e.bus.Publish(event.CallEnded{Call: p.id, Result: out, Took: time.Since(start)})
	p.finish(formatResult(p.cmd, out))
}

// runSafely turns a panicking Runner into a failed Call. Taking the
// session down would lose every checkpoint still rollable.
func runSafely(ctx context.Context, r Runner, cmd string, lines chan<- capture.StreamEvent) (res capture.Result, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("runner panicked: %v", v)
		}
	}()
	return r.Run(ctx, cmd, lines)
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

// why says what stopped a Call. The time limit is named with its length,
// so the model can tell a slow command from a broken one.
func (e *Engine) why(ctx, cctx context.Context, err error) string {
	if errors.Is(cctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		return "stopped after " + model.Brief(e.commandTimeout) + ", still running"
	}
	return err.Error()
}

// formatResult is what the model reads back.
func formatResult(cmd string, r event.Result) string {
	var b strings.Builder
	if r.Err != "" {
		// What it printed before it was stopped is often why.
		fmt.Fprintf(&b, "Could not run `%s`: %s", cmd, r.Err)
		if r.Stdout != "" || r.Stderr != "" {
			b.WriteString(". Output so far:")
			writeOutput(&b, r)
		}
		return b.String()
	}
	fmt.Fprintf(&b, "Exit code %d.", r.ExitCode)
	if r.Stdout == "" && r.Stderr == "" {
		b.WriteString(" No output.")
	}
	writeOutput(&b, r)
	return b.String()
}

func writeOutput(b *strings.Builder, r event.Result) {
	if r.Stdout != "" {
		fmt.Fprintf(b, "\nstdout:\n%s", r.Stdout)
	}
	if r.Stderr != "" {
		fmt.Fprintf(b, "\nstderr:\n%s", r.Stderr)
	}
	if r.Truncated {
		b.WriteString("\n[output truncated at capture]")
	}
}

// renders asks the tool how its output should be read. Advisory: a
// front-end may ignore it.
func (e *Engine) renders(name string) string {
	t, ok := e.tools.Lookup(name)
	if !ok {
		return ""
	}
	return t.Describe().Renders
}

// executor names what runs a tool, empty for a shell command. A
// front-end needs it to say what a rollback cannot take back.
func (e *Engine) executor(name string) string {
	t, ok := e.tools.Lookup(name)
	if !ok {
		return ""
	}
	return t.Describe().Executor
}
