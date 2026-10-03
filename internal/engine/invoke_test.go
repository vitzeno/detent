package engine

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// A tool call with an executor goes to the Invoker, and the Runner never
// sees it: there is no command for a shell to run.
func TestExecute_ARemoteToolCallGoesToTheInvoker(t *testing.T) {
	in := &fakeInvoker{out: capture.Result{Stdout: "from the server\n"}}
	r := remoteRig(t, in, []model.Reply{{Requests: []event.ToolRequest{remoteCall("r1")}}})
	r.approve(t)
	r.run("go")

	require.Len(t, in.calls(), 1)
	assert.Equal(t, "srv", in.calls()[0].Executor)
	assert.Empty(t, r.runner.commands(), "the Runner ran a Call with no command")

	ended := r.of(event.ToolCallEndedKind)
	require.Len(t, ended, 1)
	assert.Equal(t, "from the server\n", ended[0].(event.ToolCallEnded).Result.Stdout)
}

// The row and the log say where it ran, or the sandbox badge is a lie.
func TestExecute_ToolCallStartedNamesTheServer(t *testing.T) {
	in := &fakeInvoker{out: capture.Result{Stdout: "ok\n"}}
	r := remoteRig(t, in, []model.Reply{{Requests: []event.ToolRequest{remoteCall("r1")}}})
	r.approve(t)
	r.run("go")

	started := r.of(event.ToolCallStartedKind)
	require.Len(t, started, 1)
	assert.Equal(t, "srv", started[0].(event.ToolCallStarted).Runner)
}

// A shell tool call must keep going to the Runner untouched.
func TestExecute_AShellToolCallStillGoesToTheRunner(t *testing.T) {
	in := &fakeInvoker{}
	r := remoteRig(t, in, []model.Reply{{Requests: []event.ToolRequest{bashCall("c1", "ls")}}})
	r.run("go")

	assert.Empty(t, in.calls(), "a shell Call reached the Invoker")
	assert.Equal(t, []string{"ls"}, r.runner.commands())
}

// Without an Invoker the tool call says so rather than running or hanging.
func TestExecute_NoInvokerIsAnAnswerNotAFailure(t *testing.T) {
	r := remoteRig(t, nil, []model.Reply{{Requests: []event.ToolRequest{remoteCall("r1")}}})
	r.approve(t)
	end := r.run("go")

	assert.Equal(t, event.EndDone, end.Reason)
	answered(t, r.eng)
	assert.Contains(t, r.eng.Transcript()[2].Content, "No invoker")
}

// A panicking Invoker must not take the session and its checkpoints.
func TestExecute_APanickingInvokerIsAFailedToolCall(t *testing.T) {
	in := &fakeInvoker{panic: true}
	r := remoteRig(t, in, []model.Reply{{Requests: []event.ToolRequest{remoteCall("r1")}}})
	r.approve(t)
	end := r.run("go")

	assert.Equal(t, event.EndDone, end.Reason)
	answered(t, r.eng)
	assert.Contains(t, r.eng.Transcript()[2].Content, "panicked")
}

// The gate itself: an MCP tool call waits for a human, and one nobody
// answers never runs. This is why the tests above have to approve.
func TestExecute_ARemoteToolCallWaitsForAHuman(t *testing.T) {
	in := &fakeInvoker{out: capture.Result{Stdout: "ok\n"}}
	r := remoteRig(t, in, []model.Reply{{Requests: []event.ToolRequest{remoteCall("r1")}}})

	// Submitted here rather than through run, which waits for the Turn
	// to end and would deadlock against the confirm.
	r.bus.Publish(event.SubmitPrompt{Text: "go"})

	asked := r.await(event.ApprovalAskedKind).(event.ApprovalAsked)
	assert.True(t, asked.Risk.Dangerous)
	assert.Empty(t, in.calls(), "it ran before anyone answered")

	r.bus.Publish(event.ResolveApproval{ToolCall: asked.ToolCall, Approved: false})
	r.awaitNth(event.TurnEndedKind, 1)
	assert.Empty(t, in.calls(), "a declined Call still reached the server")
}

// Declining stops a tool call, not a Turn: its siblings still run.
func TestExecute_DecliningARemoteToolCallLeavesItsSiblings(t *testing.T) {
	in := &fakeInvoker{out: capture.Result{Stdout: "ok\n"}}
	r := remoteRig(t, in, []model.Reply{{Requests: []event.ToolRequest{
		remoteCall("r1"), bashCall("c1", "echo still here"),
	}}})

	go func() {
		if asked, ok := r.waitNth(event.ApprovalAskedKind, 1); ok {
			r.bus.Publish(event.ResolveApproval{ToolCall: asked.(event.ApprovalAsked).ToolCall, Approved: false})
		}
	}()
	end := r.run("go")

	assert.Equal(t, event.EndDone, end.Reason)
	assert.Empty(t, in.calls())
	assert.Equal(t, []string{"echo still here"}, r.runner.commands())
}

// fakeInvoker answers a tool call the way a connected server would.
type fakeInvoker struct {
	mu    sync.Mutex
	saw   []tool.Call
	out   capture.Result
	panic bool
}

func (f *fakeInvoker) Invoke(_ context.Context, c tool.Call) capture.Result {
	f.mu.Lock()
	f.saw = append(f.saw, c)
	f.mu.Unlock()
	if f.panic {
		panic("invoker exploded")
	}
	return f.out
}

func (f *fakeInvoker) calls() []tool.Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tool.Call(nil), f.saw...)
}

// remoteTool is one an Invoker answers, not a Runner.
type remoteTool struct{ name string }

func (r remoteTool) Name() string { return r.name }
func (r remoteTool) Describe() tool.Spec {
	return tool.Spec{Description: "a remote thing", Executor: "srv",
		Raw: map[string]any{"type": "object"}}
}
func (r remoteTool) Lower(a tool.Args) (string, error) { return event.Command(r.name, a), nil }

func remoteRig(t *testing.T, in Invoker, replies []model.Reply, opts ...Option) *rig {
	t.Helper()
	reg := tool.Standard()
	require.NoError(t, reg.Register(remoteTool{name: "srv__do"}))
	if in != nil {
		opts = append(opts, WithInvoker(in))
	}
	return rigWithTools(t, event.New(), &fakeModel{replies: replies},
		&fakeRunner{out: "shell ran\n"}, reg, opts...)
}

func remoteCall(id string) event.ToolRequest {
	return event.ToolRequest{ID: id, Name: "srv__do", Args: map[string]any{"x": "1"}}
}

// approve answers the confirm the mcp floor forces. No assessor can
// do this instead: Widen never narrows, which is the whole point.
func (r *rig) approve(t *testing.T) {
	t.Helper()
	go func() {
		if asked, ok := r.waitNth(event.ApprovalAskedKind, 1); ok {
			r.bus.Publish(event.ResolveApproval{ToolCall: asked.(event.ApprovalAsked).ToolCall, Approved: true})
		}
	}()
}
