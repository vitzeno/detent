package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/model"
)

// Read-only Calls run together; anything else runs alone and in the
// order the model asked. Mutability decides parallelism and, now that
// checkpointing is per Turn, nothing else.
func TestStep_ReadOnlyCallsRunTogether(t *testing.T) {
	r := newRig(t, []model.Reply{{Calls: []event.ToolCall{
		readCall("c1", "a.go"), readCall("c2", "b.go"), readCall("c3", "c.go"),
	}}})
	r.runner.mu.Lock()
	r.runner.hold = make(chan struct{})
	r.runner.mu.Unlock()

	r.bus.Publish(event.SubmitPrompt{Text: "read three files"})
	require.Eventually(t, func() bool { return len(r.of(event.CallStartedKind)) == 3 },
		2*time.Second, 5*time.Millisecond, "all three should be in flight at once")

	close(r.runner.hold)
	r.await(event.TurnEndedKind)
	answered(t, r.eng)
}

func TestStep_WritesRunOneAtATime(t *testing.T) {
	r := newRig(t, []model.Reply{{Calls: []event.ToolCall{
		bashCall("c1", "touch a"), bashCall("c2", "touch b"), bashCall("c3", "touch c"),
	}}})
	r.run("make three files")
	assert.Equal(t, []string{"touch a", "touch b", "touch c"}, r.runner.commands(),
		"serial, in the order asked")
}

func TestStep_CapsHowManyCallsOneStepMayAskFor(t *testing.T) {
	var calls []event.ToolCall
	for i := range 6 {
		calls = append(calls, readCall(string(rune('a'+i)), "f.go"))
	}
	r := newRig(t, []model.Reply{{Calls: calls}}, WithCallsPerStep(3))
	r.run("read everything")

	assert.Len(t, r.runner.commands(), 3)
	answered(t, r.eng)

	var refused int
	for _, m := range r.eng.Transcript() {
		if m.Role == event.RoleTool && contains(m.Content, "at most 3 calls") {
			refused++
		}
	}
	assert.Equal(t, 3, refused, "the rest are told why, not silently dropped")
}

func TestStep_ParallelismIsCapped(t *testing.T) {
	var calls []event.ToolCall
	for i := range 8 {
		calls = append(calls, readCall(string(rune('a'+i)), "f.go"))
	}
	r := newRig(t, []model.Reply{{Calls: calls}}, WithParallelCalls(2))
	r.runner.mu.Lock()
	r.runner.hold = make(chan struct{})
	r.runner.mu.Unlock()

	r.bus.Publish(event.SubmitPrompt{Text: "read them"})
	require.Eventually(t, func() bool { return len(r.of(event.CallStartedKind)) == 2 },
		2*time.Second, 5*time.Millisecond)

	time.Sleep(50 * time.Millisecond)
	assert.Len(t, r.of(event.CallStartedKind), 2, "no more than the cap may be in flight")
	close(r.runner.hold)
	r.await(event.TurnEndedKind)
}

// Only a Dangerous Call is shown. Everything else runs straight
// through, which is the harness's whole posture.
func TestStep_OnlyDangerousCallsAreShown(t *testing.T) {
	r := newRig(t, []model.Reply{{Calls: []event.ToolCall{
		bashCall("c1", "ls -la"), readCall("c2", "a.go"),
	}}})
	r.run("look around")
	assert.Empty(t, r.of(event.ApprovalAskedKind))
	assert.Len(t, r.runner.commands(), 2)
}

func TestStep_ApprovalLetsADangerousCallThrough(t *testing.T) {
	r := newRig(t, []model.Reply{{Calls: []event.ToolCall{bashCall("c1", "rm -rf build")}}})
	r.bus.Publish(event.SubmitPrompt{Text: "clean"})

	asked := r.await(event.ApprovalAskedKind).(event.ApprovalAsked)
	r.bus.Publish(event.ResolveApproval{Call: asked.Call, Approved: true})
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

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
