package humanshell

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/host"
)

// Everything here drives the package with a bus and a fake Runner, no
// engine and no TUI.

func TestWatch_RunsACommandAndTellsTheModel(t *testing.T) {
	bus := event.New()
	c := collect(t, bus)
	r := &fakeRunner{out: "M ui/keys.go\n", lines: []string{"M ui/keys.go"}}
	stop := Watch(t.Context(), bus, r, "sandbox")
	t.Cleanup(stop)

	bus.Publish(event.RunCommand{Text: "  git status --short  "})

	started := c.await(t, event.ShellStartedKind).(event.ShellStarted)
	assert.Equal(t, "git status --short", started.Command, "trimmed, since a stray space is not the command")
	assert.Equal(t, "sandbox", started.Runner)

	ended := c.await(t, event.ShellEndedKind).(event.ShellEnded)
	assert.Equal(t, started.Shell, ended.Shell, "one Shell, correlated by id")
	assert.Equal(t, "M ui/keys.go\n", ended.Result.Stdout)

	note := c.await(t, event.NoteContextKind).(event.NoteContext)
	assert.Equal(t, "[human ran a command in the sandbox]\n$ git status --short\nexit 0\nM ui/keys.go", note.Text)

	chunks := c.of(event.OutputChunkKind)
	require.Len(t, chunks, 1, "live output rides on OutputChunk, keyed by the Shell's id")
	assert.Equal(t, started.Shell, chunks[0].(event.OutputChunk).Call)
	assert.Equal(t, []string{"git status --short"}, r.commands())
}

// The message is an intent, not a fact: only the engine may touch the
// transcript.
func TestWatch_AsksTheEngineToAppendRatherThanAppending(t *testing.T) {
	bus := event.New()
	c := collect(t, bus)
	stop := Watch(t.Context(), bus, &fakeRunner{}, "host")
	t.Cleanup(stop)

	bus.Publish(event.RunCommand{Text: "pwd"})
	c.await(t, event.NoteContextKind)

	for _, k := range c.kinds() {
		assert.NotEqual(t, event.AppendedKind, k, "humanshell never writes the transcript itself")
	}
}

func TestWatch_CancelStopsTheRunningCommand(t *testing.T) {
	bus := event.New()
	c := collect(t, bus)
	r := &fakeRunner{hold: make(chan struct{})}
	stop := Watch(t.Context(), bus, r, "host")
	t.Cleanup(stop)

	bus.Publish(event.RunCommand{Text: "sleep 45"})
	started := c.await(t, event.ShellStartedKind).(event.ShellStarted)

	bus.Publish(event.CancelCommand{Shell: started.Shell})
	ended := c.await(t, event.ShellEndedKind).(event.ShellEnded)
	assert.Equal(t, stoppedByHuman, ended.Result.Err,
		"the model reads why it stopped, not a context error")

	note := c.await(t, event.NoteContextKind).(event.NoteContext)
	assert.Contains(t, note.Text, "did not finish: stopped by the human")
}

// A zero id means whichever command is running, which is what esc
// knows without the front-end tracking one.
func TestWatch_CancelWithNoIdStopsWhateverRuns(t *testing.T) {
	bus := event.New()
	c := collect(t, bus)
	stop := Watch(t.Context(), bus, &fakeRunner{hold: make(chan struct{})}, "host")
	t.Cleanup(stop)

	bus.Publish(event.RunCommand{Text: "sleep 45"})
	c.await(t, event.ShellStartedKind)

	bus.Publish(event.CancelCommand{})
	assert.Equal(t, stoppedByHuman, c.await(t, event.ShellEndedKind).(event.ShellEnded).Result.Err)
}

// Refused rather than queued: the human can see the first one running
// and decide for themselves.
func TestWatch_RefusesASecondCommandWhileOneRuns(t *testing.T) {
	bus := event.New()
	c := collect(t, bus)
	r := &fakeRunner{hold: make(chan struct{})}
	stop := Watch(t.Context(), bus, r, "host")
	t.Cleanup(stop)

	bus.Publish(event.RunCommand{Text: "sleep 45"})
	c.await(t, event.ShellStartedKind)
	bus.Publish(event.RunCommand{Text: "ls"})

	notice := c.await(t, event.NoticeKind).(event.Notice)
	assert.Equal(t, "warn", notice.Level)
	assert.Contains(t, notice.Text, "already running")
	assert.Len(t, c.of(event.ShellStartedKind), 1, "and nothing second started")
	assert.Equal(t, []string{"sleep 45"}, r.commands())
}

// Stop must not return until the last facts are on the bus, or they
// land after the drain and nothing records them.
func TestWatch_StopPublishesTheEndBeforeItReturns(t *testing.T) {
	bus := event.New()
	c := collect(t, bus)
	stop := Watch(t.Context(), bus, &fakeRunner{hold: make(chan struct{})}, "host")

	bus.Publish(event.RunCommand{Text: "sleep 45"})
	c.await(t, event.ShellStartedKind)

	stop()
	bus.Publish(event.Notice{Level: "info", Text: "after the stop"})
	bus.Settle(2 * time.Second)

	ended := c.recordOf(t, event.ShellEndedKind)
	after := c.recordOf(t, event.NoticeKind)
	assert.Less(t, ended.Ordinal, after.Ordinal,
		"the end was published while stop was still running")
}

// The session ending stops the human's command without anyone calling stop.
func TestWatch_ItsContextEndingStopsTheCommand(t *testing.T) {
	bus := event.New()
	c := collect(t, bus)
	ctx, cancel := context.WithCancel(t.Context())
	defer Watch(ctx, bus, &fakeRunner{hold: make(chan struct{})}, "host")()

	bus.Publish(event.RunCommand{Text: "sleep 45"})
	c.await(t, event.ShellStartedKind)
	cancel()
	c.await(t, event.ShellEndedKind)
}

func TestWatch_EmptyCommandDoesNothing(t *testing.T) {
	bus := event.New()
	c := collect(t, bus)
	r := &fakeRunner{}
	stop := Watch(t.Context(), bus, r, "host")

	bus.Publish(event.RunCommand{Text: "   "})
	stop()
	// The intent itself is on the bus, but no fact saying a command ran.
	assert.Empty(t, c.of(event.ShellStartedKind))
	assert.Empty(t, c.of(event.ShellEndedKind))
	assert.Empty(t, c.of(event.NoteContextKind))
	assert.Empty(t, r.commands())
}

// A panicking Runner is a failed command, not a dead session.
func TestWatch_APanickingRunnerEndsTheCommandOnly(t *testing.T) {
	bus := event.New()
	c := collect(t, bus)
	stop := Watch(t.Context(), bus, &fakeRunner{boom: true}, "host")
	t.Cleanup(stop)

	bus.Publish(event.RunCommand{Text: "boom"})
	ended := c.await(t, event.ShellEndedKind).(event.ShellEnded)
	assert.Contains(t, ended.Result.Err, "runner panicked")

	bus.Publish(event.RunCommand{Text: "after"})
	assert.Eventually(t, func() bool { return len(c.of(event.ShellStartedKind)) == 2 },
		3*time.Second, time.Millisecond, "the subscriber is still listening")
}

// host.Shell caps a command at 30s when handed no deadline, which no
// build, test run or install finishes inside.
func TestWatch_DoesNotInheritTheTimeoutMeantForTheModel(t *testing.T) {
	bus := event.New()
	c := collect(t, bus)
	r := &fakeRunner{}
	stop := Watch(t.Context(), bus, r, "host")
	t.Cleanup(stop)

	bus.Publish(event.RunCommand{Text: "go test ./..."})
	c.await(t, event.ShellEndedKind)

	r.mu.Lock()
	defer r.mu.Unlock()
	assert.Greater(t, r.left, host.DefaultTimeout,
		"a deadline is set here precisely so host.Shell does not apply its own")
	assert.LessOrEqual(t, r.left, shellMax, "but it is still bounded")
}

// Against the real runner, because a fake that closes its output
// channel correctly hides a package that depends on it doing so.
func TestWatch_AgainstTheRealHostShell(t *testing.T) {
	bus := event.New()
	c := collect(t, bus)
	stop := Watch(t.Context(), bus, host.NewShell(), "host")
	t.Cleanup(stop)

	bus.Publish(event.RunCommand{Text: "printf 'one\\ntwo\\n'; printf 'oops\\n' >&2; exit 3"})

	ended := c.await(t, event.ShellEndedKind).(event.ShellEnded)
	assert.Equal(t, 3, ended.Result.ExitCode)
	assert.Equal(t, "one\ntwo\n", ended.Result.Stdout)
	assert.Equal(t, "oops\n", ended.Result.Stderr)
	assert.Empty(t, ended.Result.Err, "a non-zero exit is a result, not an error")

	note := c.await(t, event.NoteContextKind).(event.NoteContext)
	assert.Equal(t, "[human ran a command on the host]\n"+
		"$ printf 'one\\ntwo\\n'; printf 'oops\\n' >&2; exit 3\n"+
		"exit 3\none\ntwo\noops", note.Text)

	var lines []string
	for _, e := range c.of(event.OutputChunkKind) {
		lines = append(lines, e.(event.OutputChunk).Line)
	}
	assert.ElementsMatch(t, []string{"one", "two", "oops"}, lines,
		"every line reached the bus, and the channel was closed")
}

// fakeRunner replies with canned output, and holds when asked.
type fakeRunner struct {
	mu    sync.Mutex
	ran   []string
	out   string
	lines []string
	exit  int
	hold  chan struct{}
	boom  bool
	left  time.Duration // how long the ctx had when Run was called
}

func (f *fakeRunner) Run(ctx context.Context, cmd string, ev chan<- capture.StreamEvent) (capture.Result, error) {
	f.mu.Lock()
	f.ran = append(f.ran, cmd)
	if d, ok := ctx.Deadline(); ok {
		f.left = time.Until(d)
	}
	hold, out, lines, boom, exit := f.hold, f.out, f.lines, f.boom, f.exit
	f.mu.Unlock()

	if boom {
		panic("runner exploded")
	}
	for _, l := range lines {
		ev <- capture.StreamEvent{Line: l}
	}
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return capture.Result{}, ctx.Err()
		}
	}
	return capture.Result{Stdout: out, ExitCode: exit}, nil
}

func (f *fakeRunner) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ran...)
}

// failingRunner returns at once with an error, having sent nothing.
type failingRunner struct{}

func (failingRunner) Run(context.Context, string, chan<- capture.StreamEvent) (capture.Result, error) {
	return capture.Result{}, errors.New("create task: already exists")
}

// The caller closes the output channel, so a Runner's early error costs
// nothing: no grace to wait out, no relay left behind.
func TestWatch_ARunnerErrorEndsTheCommandAtOnce(t *testing.T) {
	bus := event.New()
	c := collect(t, bus)
	stop := Watch(t.Context(), bus, failingRunner{}, "sandbox")
	t.Cleanup(stop)

	start := time.Now()
	bus.Publish(event.RunCommand{Text: "ls"})
	ended := c.await(t, event.ShellEndedKind).(event.ShellEnded)
	assert.Less(t, time.Since(start), time.Second)
	assert.Equal(t, "create task: already exists", ended.Result.Err)
}

// collector keeps Records, so a test can assert on order as well as
// on content.
type collector struct {
	mu  sync.Mutex
	got []event.Record
}

func collect(t *testing.T, bus *event.Bus) *collector {
	t.Helper()
	c := &collector{}
	records, unsub := bus.Subscribe(nil)
	go func() {
		for rec := range records {
			c.mu.Lock()
			c.got = append(c.got, rec)
			c.mu.Unlock()
		}
	}()
	t.Cleanup(func() { unsub(); bus.Close() })
	return c
}

func (c *collector) await(t *testing.T, k event.Kind) event.Event {
	t.Helper()
	var found event.Event
	require.Eventuallyf(t, func() bool {
		for _, e := range c.of(k) {
			found = e
			return true
		}
		return false
	}, 3*time.Second, time.Millisecond, "waiting for %s; saw %v", k, c.kinds())
	return found
}

func (c *collector) recordOf(t *testing.T, k event.Kind) event.Record {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.got {
		if r.Event.Kind() == k {
			return r
		}
	}
	t.Fatalf("no %s was published", k)
	return event.Record{}
}

func (c *collector) of(k event.Kind) []event.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []event.Event
	for _, r := range c.got {
		if r.Event.Kind() == k {
			out = append(out, r.Event)
		}
	}
	return out
}

func (c *collector) kinds() []event.Kind {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]event.Kind, 0, len(c.got))
	for _, r := range c.got {
		out = append(out, r.Event.Kind())
	}
	return out
}
