package headless

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

func TestRun(t *testing.T) {
	turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, c := range []struct {
		name     string
		approve  Approver
		approved bool
	}{
		{"approve everything", AutoApprove, true},
		{"decline everything", AutoDecline, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			bus := event.New()
			t.Cleanup(bus.Close)
			got := make(chan event.Event, 8)
			fakeEngine(t, bus, func(ev event.Event) {
				got <- ev
				switch v := ev.(type) {
				case event.SubmitPrompt:
					bus.Publish(event.SessionStarted{Model: "m", Sandbox: true})
					bus.Publish(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "rm -rf x"}})
					bus.Publish(event.ApprovalAsked{ToolCall: call, Tool: "bash"})
				case event.ResolveApproval:
					bus.Publish(event.BoundReached{Turn: turn, Steps: 3})
				case event.Continue:
					assert.Equal(t, turn, v.Turn)
					bus.Publish(event.TurnEnded{Turn: turn, Reason: event.EndBound})
				}
			})

			var out, errOut bytes.Buffer
			p := New(bus, c.approve, &out, &errOut)
			reason := p.Run(t.Context(), "clean up")
			assert.Equal(t, event.EndBound, reason)

			require.Equal(t, event.SubmitPrompt{Text: "clean up"}, <-got)
			assert.Equal(t, event.ResolveApproval{ToolCall: call, Approved: c.approved}, <-got)
			assert.Equal(t, event.Continue{Turn: turn, Approved: false}, <-got, "the bound is where an unattended run stops")
			assert.Contains(t, out.String(), "→ rm -rf x")
			assert.Contains(t, errOut.String(), "commands run on the sandbox")
		})
	}
}

func TestRun_ACancelIsAnAbort(t *testing.T) {
	bus := event.New()
	t.Cleanup(bus.Close)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.Equal(t, event.EndAborted, New(bus, AutoDecline, io.Discard, io.Discard).Run(ctx, "go"))
}

func TestRun_AClosedBusIsAnError(t *testing.T) {
	bus := event.New()
	p := New(bus, AutoDecline, io.Discard, io.Discard)
	bus.Close()
	done := make(chan event.EndReason, 1)
	go func() { done <- p.Run(t.Context(), "go") }()
	select {
	case r := <-done:
		assert.Equal(t, event.EndError, r)
	case <-time.After(3 * time.Second):
		t.Fatal("Run never noticed the bus closed")
	}
}

func TestAsk(t *testing.T) {
	cases := []struct {
		name string
		in   io.Reader
		want bool
		says string
	}{
		{"y", strings.NewReader("y\n"), true, ""},
		{"capital Y", strings.NewReader("Y\n"), true, ""},
		{"padded", strings.NewReader(" y \n"), true, ""},
		{"no", strings.NewReader("n\n"), false, ""},
		{"yes is not y", strings.NewReader("yes\n"), false, ""},
		{"empty line", strings.NewReader("\n"), false, ""},
		{"y at EOF", strings.NewReader("y"), true, ""},
		{"closed stdin", strings.NewReader(""), false, "stdin is closed"},
		{"unreadable", brokenReader{}, false, "could not read a decision"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			got := Ask(c.in, &out)(event.ApprovalAsked{Tool: "bash", Args: map[string]any{"command": "rm -rf x"}})
			assert.Equal(t, c.want, got)
			assert.Contains(t, out.String(), "rm -rf x", "the human sees the literal command")
			assert.Contains(t, out.String(), c.says)
			assert.NotContains(t, out.String(), "flagged:", "no rationale, no line for one")
		})
	}
}

func TestAsk_ShowsWhyItWasFlagged(t *testing.T) {
	var out bytes.Buffer
	Ask(strings.NewReader("n\n"), &out)(event.ApprovalAsked{Tool: "bash",
		Args: map[string]any{"command": "sudo x"}, Rationale: "runs as root"})
	assert.Contains(t, out.String(), "flagged: runs as root")
}

func TestAsk_ShowsControlsInTheCommandEscaped(t *testing.T) {
	var out bytes.Buffer
	Ask(strings.NewReader("n\n"), &out)(event.ApprovalAsked{Tool: "bash",
		Args: map[string]any{"command": hostile}, Rationale: "flag " + hostile})
	assertDefused(t, out.String())
	assert.Contains(t, out.String(), "flagged: flag echo hi^[[2J")
}

func TestHandle_DefusesWhatAModelOrCommandWrote(t *testing.T) {
	cases := []struct {
		name string
		ev   event.Event
		err  bool
	}{
		{"proposed call", event.ToolCallProposed{Tool: "bash", Args: map[string]any{"command": hostile}}, false},
		{"model text", event.ModelText{Text: hostile}, false},
		{"call ended with an error", event.ToolCallEnded{Result: event.Result{Err: hostile}}, false},
		{"notice", event.Notice{Level: "warn", Text: hostile}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			p := &Printer{bus: event.New(), out: &out, errOut: &errOut}
			t.Cleanup(p.bus.Close)
			p.handle(c.ev)
			got := out.String()
			if c.err {
				got = errOut.String()
			}
			assertDefused(t, got)
		})
	}
}

func TestClip(t *testing.T) {
	assert.Equal(t, "ls", clip("ls", 10))
	assert.Equal(t, "éééé…", clip(strings.Repeat("é", 8), 4), "cut by rune, never mid-character")
	assert.Equal(t, "cat <<EOF …", clip("cat <<EOF\nbody\nEOF", 40))
}

// fakeEngine answers intents the way the engine would, enough for one Turn.
func fakeEngine(t *testing.T, bus *event.Bus, script func(ev event.Event)) {
	t.Helper()
	intents, unsub := bus.Subscribe(event.Intents())
	t.Cleanup(unsub)
	go func() {
		for rec := range intents {
			script(rec.Event)
		}
	}()
}

// What the model wrote is shown escaped, never sent to the terminal it is asking on.
const hostile = "echo hi\x1b[2J\x1b]52;c;cm0gLXJmIH4=\x07\rrm -rf ~ \u202etxt.exe"

func assertDefused(t *testing.T, got string) {
	t.Helper()
	for _, raw := range []string{"\x1b", "\x07", "\r", "\u202e"} {
		assert.NotContains(t, got, raw)
	}
	for _, shown := range []string{"^[[2J", "^[]52;c;cm0gLXJmIH4=^G", "^Mrm -rf ~", `\u202etxt.exe`} {
		assert.Contains(t, got, shown)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("tty gone") }
