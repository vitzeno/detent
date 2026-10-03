package engine

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// Read-only tool calls run together, anything else runs alone and in the
// order the model asked.
func TestStep_ReadOnlyToolCallsRunTogether(t *testing.T) {
	r := newRig(t, []model.Reply{{Requests: []event.ToolRequest{
		readCall("c1", "a.go"), readCall("c2", "b.go"), readCall("c3", "c.go"),
	}}})
	r.runner.mu.Lock()
	r.runner.hold = make(chan struct{})
	r.runner.mu.Unlock()

	r.bus.Publish(event.SubmitPrompt{Text: "read three files"})
	require.Eventually(t, func() bool { return len(r.of(event.ToolCallStartedKind)) == 3 },
		2*time.Second, 5*time.Millisecond, "all three should be in flight at once")

	close(r.runner.hold)
	r.await(event.TurnEndedKind)
	answered(t, r.eng)
}

func TestStep_WritesRunOneAtATime(t *testing.T) {
	r := newRig(t, []model.Reply{{Requests: []event.ToolRequest{
		bashCall("c1", "touch a"), bashCall("c2", "touch b"), bashCall("c3", "touch c"),
	}}})
	r.run("make three files")
	assert.Equal(t, []string{"touch a", "touch b", "touch c"}, r.runner.commands(),
		"serial, in the order asked")
}

func TestStep_CapsHowManyToolCallsOneStepMayAskFor(t *testing.T) {
	var calls []event.ToolRequest
	for i := range 6 {
		calls = append(calls, readCall(string(rune('a'+i)), string(rune('a'+i))+".go"))
	}
	r := newRig(t, []model.Reply{{Requests: calls}}, WithToolCallsPerStep(3))
	r.run("read everything")

	assert.Len(t, r.runner.commands(), 3)
	answered(t, r.eng)

	var refused int
	for _, m := range r.eng.messages() {
		if m.Role == event.RoleTool && strings.Contains(m.Content, "at most 3 calls") {
			refused++
		}
	}
	assert.Equal(t, 3, refused, "the rest are told why, not silently dropped")
}

// Only a Dangerous tool call is shown. Everything else runs straight
// through, which is the harness's whole posture.
func TestStep_OnlyDangerousToolCallsAreShown(t *testing.T) {
	r := newRig(t, []model.Reply{{Requests: []event.ToolRequest{
		bashCall("c1", "ls -la"), readCall("c2", "a.go"),
	}}})
	r.run("look around")
	assert.Empty(t, r.of(event.ApprovalAskedKind))
	assert.Len(t, r.runner.commands(), 2)
}

func TestStep_ApprovalLetsADangerousToolCallThrough(t *testing.T) {
	r := newRig(t, []model.Reply{{Requests: []event.ToolRequest{bashCall("c1", "rm -rf build")}}})
	r.bus.Publish(event.SubmitPrompt{Text: "clean"})

	asked := r.await(event.ApprovalAskedKind).(event.ApprovalAsked)
	r.bus.Publish(event.ResolveApproval{ToolCall: asked.ToolCall, Approved: true})
	r.await(event.TurnEndedKind)

	assert.Equal(t, []string{"rm -rf build"}, r.runner.commands())
	answered(t, r.eng)
}

func TestFormatResult_SaysWhatHappened(t *testing.T) {
	tests := []struct {
		name string
		res  event.Result
		want []string
	}{
		{"output", event.Result{Stdout: "hello"}, []string{"Exit code 0", "stdout", "hello"}},
		{"silence", event.Result{}, []string{"Exit code 0", "No output"}},
		{"failure", event.Result{ExitCode: 1, Stderr: "nope"}, []string{"Exit code 1", "stderr", "nope"}},
		{"could not run", event.Result{Err: "no such file"}, []string{"Could not run", "no such file"}},
		{"truncated", event.Result{Stdout: "x", Truncated: true}, []string{"truncated at capture"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatResult("cmd", tt.res)
			for _, w := range tt.want {
				assert.Contains(t, got, w)
			}
		})
	}
}

// A command that runs too long is stopped, and the model is told how long
// it waited and what it printed, so it can tell slow from broken.
func TestStep_ACommandPastItsLimitIsStoppedAndSaysSo(t *testing.T) {
	r := newRig(t, []model.Reply{{Requests: []event.ToolRequest{bashCall("c1", "make build")}}},
		WithCommandTimeout(50*time.Millisecond))
	r.runner.mu.Lock()
	r.runner.hold, r.runner.partial = make(chan struct{}), "compiling 1 of 40\n"
	r.runner.mu.Unlock()

	r.run("build it")
	ended := r.of(event.ToolCallEndedKind)[0].(event.ToolCallEnded)
	assert.Equal(t, "stopped after 50ms, still running", ended.Result.Err)
	var answer string
	for _, m := range r.eng.messages() {
		if m.Role == event.RoleTool {
			answer = m.Content
		}
	}
	assert.Contains(t, answer, "stopped after 50ms, still running. Output so far:")
	assert.Contains(t, answer, "compiling 1 of 40")
}

// On the host a native tool runs in this process and never reaches the
// runner, while bash still does. In the sandbox both are commands.
func TestStep_OnTheHostANativeToolRunsHere(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("notes.txt", []byte("from the file\n"), 0o600))
	runner := &fakeRunner{out: "from the runner"}
	fm := &fakeModel{replies: []model.Reply{
		{Requests: []event.ToolRequest{readCall("c1", "notes.txt"), bashCall("c2", "date")}},
		{Text: "done"},
	}}
	r := rigOn(t, event.New(), fm, runner, hostSelector{runner}, tool.Standard())
	r.run("read it")

	assert.Equal(t, []string{"date"}, runner.commands(), "only bash went to the runner")
	var read event.ToolCallEnded
	for _, e := range r.of(event.ToolCallEndedKind) {
		if ended, ok := e.(event.ToolCallEnded); ok && strings.Contains(ended.Result.Stdout, "from the file") {
			read = ended
		}
	}
	assert.Equal(t, "from the file\n", read.Result.Stdout, "read_file read the real file")
	started := r.of(event.ToolCallStartedKind)
	require.NotEmpty(t, started)
	assert.Equal(t, hostMode, started[0].(event.ToolCallStarted).Runner)
}

// A native tool past its limit is stopped and reported as a command would be.
func TestStep_ANativeToolPastItsLimitIsStoppedAndSaysSo(t *testing.T) {
	fm := &fakeModel{replies: []model.Reply{
		{Requests: []event.ToolRequest{{ID: "c1", Name: "slow", Args: map[string]any{}}}},
		{Text: "done"},
	}}
	runner := &fakeRunner{}
	r := rigOn(t, event.New(), fm, runner, hostSelector{runner}, tool.Standard(slowNative{}),
		WithCommandTimeout(50*time.Millisecond))
	r.run("wait")

	ended := r.of(event.ToolCallEndedKind)[0].(event.ToolCallEnded)
	assert.Equal(t, "stopped after 50ms, still running", ended.Result.Err)
	assert.Equal(t, "read 3 of 9\n", ended.Result.Stdout, "what it read before it was stopped is kept")
}

// slowNative is a native tool that runs until its context ends.
type slowNative struct{}

func (slowNative) Name() string { return "slow" }

func (slowNative) Describe() tool.Spec {
	return tool.Spec{Description: "waits", Mutability: event.MutRead}
}

func (slowNative) Lower(tool.Args) (string, error) { return "sleep 600", nil }

func (slowNative) Run(ctx context.Context, _ tool.Args) capture.Result {
	<-ctx.Done()
	return capture.Result{ExitCode: 1, Stdout: "read 3 of 9\n"}
}
