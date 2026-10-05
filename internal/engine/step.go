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

// hostMode is what routing names this machine.
const hostMode = "host"

// toolCallPlan is one tool call's journey. answer is always set by the end,
// which is what keeps the transcript well formed.
type toolCallPlan struct {
	id     uuid.UUID
	call   event.ToolRequest
	cmd    string
	risk   event.Risk
	answer string
	done   bool
	// ended is set once CallEnded is out, so every proposed row finishes.
	ended bool
	// prepared is the registry's lowering, and says which executor runs it.
	prepared tool.Call
}

func (p *toolCallPlan) finish(answer string) { p.answer, p.done = answer, true }

// readOnly is what the tool declares and nothing widened. A hook can
// lower unknown to read-only, so the verdict alone cannot decide this.
func (p *toolCallPlan) readOnly() bool {
	return p.prepared.Mutability == event.MutRead && p.risk.ReadOnly()
}

// approval is how a confirm ended: an abort is not the human saying no.
type approval int

const (
	declined approval = iota
	approved
	abandoned
)

// runStep answers every tool call however it went, in the order asked.
// Contiguous read-only tool calls run together, never ahead of an earlier write.
func (e *Engine) runStep(ctx context.Context, t *turnState, a *agent, step uuid.UUID, reply model.Reply) []string {
	plans := e.plan(ctx, a, step, reply)
	stopping := func() bool { return t.aborted.Load() || ctx.Err() != nil }

	var batch []*toolCallPlan
	flush := func() {
		if len(batch) > 0 && !stopping() {
			e.runParallel(ctx, a, batch)
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
			switch e.approve(ctx, t, a, p) {
			case approved:
			case declined:
				p.finish("The human declined this call. Do not repeat it; try something else.")
				continue
			case abandoned:
				break serial
			}
		}
		e.execute(ctx, a, p)
		if !p.readOnly() {
			t.changed.Store(true)
		}
	}
	flush()

	out := make([]string, len(plans))
	for i, p := range plans {
		if !p.done {
			p.finish("This call was not run: the request was aborted.")
		}
		if !p.ended {
			e.bus.Publish(event.ToolCallEnded{ToolCall: p.id, Result: event.Result{Err: p.answer}})
		}
		out[i] = p.answer
	}
	return out
}

// plan validates and assesses, settling anything that cannot run.
// Every failure here is an answer the model reads, never a Go error.
func (e *Engine) plan(ctx context.Context, a *agent, step uuid.UUID, reply model.Reply) []*toolCallPlan {
	out := make([]*toolCallPlan, 0, len(reply.Requests))
	ids := make(map[string]bool, len(reply.Requests))
	for i, c := range reply.Requests {
		p := &toolCallPlan{id: uuid.Must(uuid.NewV7()), call: c}
		out = append(out, p)
		e.bus.Publish(event.ToolCallProposed{ToolCall: p.id, Step: step, Tool: c.Name, Args: c.Args,
			Renders: renders(a, c.Name), Executor: executor(a, c.Name), Agent: a.id})

		dup := ids[c.ID]
		ids[c.ID] = true
		switch {
		case c.Err != "":
			p.finish(c.Err + ". Send the arguments as valid JSON matching the schema.")
			continue
		case dup:
			p.finish(fmt.Sprintf("Not run: another call in this step has the id %q. Give each call its own id.", c.ID))
			continue
		case i >= e.maxToolCalls:
			p.finish(fmt.Sprintf("Not run: a step may ask for at most %d calls. Ask again in the next step.", e.maxToolCalls))
			continue
		}

		prepared, err := a.tools.Prepare(c.Name, c.Args)
		if err != nil {
			p.finish(err.Error())
			continue
		}
		p.prepared, p.cmd = prepared, prepared.Command
		if n, refused := a.repeat.refuses(prepared.Command); refused {
			p.finish(fmt.Sprintf("Not run: this exact command has already run %d times in this request and printed the same thing each time. Try something else.", n))
			continue
		}
		p.risk = e.assess(ctx, a, prepared)
		e.bus.Publish(event.ToolCallAssessed{ToolCall: p.id, Risk: p.risk})
	}
	return out
}

// renders asks the tool how its output should be read. Advisory: a
// front-end may ignore it.
func renders(a *agent, name string) event.RenderKind {
	t, ok := a.tools.Lookup(name)
	if !ok {
		return ""
	}
	return t.Describe().Renders
}

// executor names what runs a tool, empty for a shell command. A
// front-end needs it to say what a rollback cannot take back.
func executor(a *agent, name string) string {
	t, ok := a.tools.Lookup(name)
	if !ok {
		return ""
	}
	return t.Describe().Executor
}

// runParallel runs read-only tool calls together. They change nothing, so
// nothing depends on their order.
func (e *Engine) runParallel(ctx context.Context, a *agent, plans []*toolCallPlan) {
	var wg sync.WaitGroup
	for _, p := range plans {
		wg.Go(func() { e.execute(ctx, a, p) })
	}
	wg.Wait()
}

// approve publishes the question and waits for its own answer, which
// dispatch delivers by tool call id. The root reads the inbox meanwhile, so
// notes typed during a long wait do not overflow it. Only the root, so
// nothing on it is taken by a child's wait.
func (e *Engine) approve(ctx context.Context, t *turnState, a *agent, p *toolCallPlan) approval {
	answer, forget := t.await(p.id)
	defer forget()
	var inbox <-chan event.Event
	if a.root() {
		inbox = t.inbox
	}
	e.bus.Publish(event.ApprovalAsked{
		ToolCall: p.id, Tool: p.call.Name, Args: p.call.Args,
		Rationale: describe(p.risk), Risk: p.risk, Agent: a.id,
	})
	for {
		select {
		case <-ctx.Done():
			return abandoned
		case ok := <-answer:
			if ok {
				return approved
			}
			return declined
		case ev := <-inbox:
			t.absorb(ev)
			if t.aborted.Load() {
				return abandoned
			}
		}
	}
}

func (e *Engine) execute(ctx context.Context, a *agent, p *toolCallPlan) {
	if p.prepared.Executor != "" {
		e.invoke(ctx, p)
		return
	}
	runner, mode := e.runners.Select(p.risk)
	// On the host a native tool runs here, the same on every OS. The
	// sandbox only has its shell, so there it runs the lowered command.
	if n, ok := a.tools.Native(p.prepared.Tool); ok && mode == hostMode {
		e.runNative(ctx, a, p, n)
		return
	}
	if runner == nil {
		p.finish("No runner is wired; nothing could be executed.")
		return
	}
	p.ended = true
	e.bus.Publish(event.ToolCallStarted{ToolCall: p.id, Runner: mode})

	lines := make(chan capture.StreamEvent, 64)
	relayed := make(chan struct{})
	go func() {
		defer close(relayed)
		for l := range lines {
			e.bus.Publish(event.OutputChunk{ToolCall: p.id, Line: l.Line, Stderr: l.Stderr})
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
	e.bus.Publish(event.ToolCallEnded{ToolCall: p.id, Result: out, Took: took})
	p.finish(formatResult(p.cmd, out))
	a.repeat.ran(p.cmd, p.answer)
}

// runNative runs a native tool in this process, bounded like a command.
func (e *Engine) runNative(ctx context.Context, a *agent, p *toolCallPlan, n tool.Native) {
	p.ended = true
	e.bus.Publish(event.ToolCallStarted{ToolCall: p.id, Runner: hostMode})
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, e.commandTimeout)
	defer cancel()
	res := nativeSafely(cctx, n, p.prepared.Args)
	out := event.Result{ExitCode: res.ExitCode, Stdout: res.Stdout, Stderr: res.Stderr, Truncated: res.Truncated}
	if err := cctx.Err(); err != nil {
		out.Err = e.why(ctx, cctx, fmt.Errorf("%s: %w", n.Name(), err))
	}
	e.bus.Publish(event.ToolCallEnded{ToolCall: p.id, Result: out, Took: time.Since(start)})
	p.finish(formatResult(event.Command(p.prepared.Tool, p.prepared.Args), out))
	a.repeat.ran(p.cmd, p.answer)
}

// invoke runs a tool call with no command. Runner names the executor, so
// the row says where it ran rather than implying the sandbox.
func (e *Engine) invoke(ctx context.Context, p *toolCallPlan) {
	if e.invoker == nil {
		p.finish("No invoker is wired for " + p.prepared.Executor + "; nothing could be executed.")
		return
	}
	p.ended = true
	e.bus.Publish(event.ToolCallStarted{ToolCall: p.id, Runner: p.prepared.Executor})

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
	e.bus.Publish(event.ToolCallEnded{ToolCall: p.id, Result: out, Took: time.Since(start)})
	p.finish(formatResult(p.cmd, out))
}

// runSafely turns a panicking Runner into a failed tool call. Taking the
// session down would lose every checkpoint still rollable.
func runSafely(ctx context.Context, r Runner, cmd string, lines chan<- capture.StreamEvent) (res capture.Result, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("runner panicked: %v", v)
		}
	}()
	return r.Run(ctx, cmd, lines)
}

// nativeSafely turns a panicking native tool into a failed one, as runSafely does for a Runner.
func nativeSafely(ctx context.Context, n tool.Native, args tool.Args) (res capture.Result) {
	defer func() {
		if v := recover(); v != nil {
			res = capture.Result{ExitCode: 1, Stderr: fmt.Sprintf("%s panicked: %v\n", n.Name(), v)}
		}
	}()
	return n.Run(ctx, args)
}

// invokeSafely turns a panicking Invoker into a failed tool call, the way
// runSafely does for a Runner.
func invokeSafely(ctx context.Context, in Invoker, c tool.Call) (res capture.Result, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("invoker panicked: %v", v)
		}
	}()
	return in.Invoke(ctx, c), nil
}

// why says what stopped a tool call. The time limit is named with its length,
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
