package viewgen_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/viewgen"
	"github.com/vitzeno/detent/ui"
)

// A view is keyed by what bash ran, not by bash, or no shipped spec
// could ever match and every command would share one.
func TestWatch_KeysOnTheCommandNotTheTool(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	g := &viewgen.Generator{Registry: ui.Registry()}
	defer g.Watch(t.Context(), bus)()

	views, unsub := bus.Subscribe(event.Only(event.ViewReadyKind))
	defer unsub()

	call := uuid.Must(uuid.NewV7())
	bus.Publish(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1})
	bus.Publish(event.CallProposed{Call: call, Tool: "bash",
		Args: map[string]any{"command": "go test ./..."}})
	bus.Publish(event.CallEnded{Call: call, Result: event.Result{Stdout: goTest}})
	bus.Publish(event.CallJudged{Call: call, RenderKind: viewgen.KindText})

	select {
	case rec := <-views:
		got, ok := rec.Event.(event.ViewReady)
		require.True(t, ok)
		require.NotNil(t, got.Spec)
		assert.Equal(t, "go test", got.Spec.Match)
		assert.Equal(t, string(viewgen.SourceShipped), got.Source)
	case <-time.After(3 * time.Second):
		t.Fatal("the shipped go test view never arrived")
	}
}

// A command the human ran is never judged, and still gets the view
// detent ships for it.
func TestWatch_AShellGetsTheShippedViewWithoutAJudge(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	g := &viewgen.Generator{Registry: ui.Registry()}
	defer g.Watch(t.Context(), bus)()

	views, unsub := bus.Subscribe(event.Only(event.ViewReadyKind))
	defer unsub()

	shell := uuid.Must(uuid.NewV7())
	bus.Publish(event.ShellStarted{Shell: shell, Command: "go test ./..."})
	bus.Publish(event.ShellEnded{Shell: shell, Result: event.Result{Stdout: goTest}})

	got := viewFor(t, views, shell)
	assert.Equal(t, "go test", got.Spec.Match)
	assert.Equal(t, string(viewgen.SourceShipped), got.Source)
}

// A Shell is asked its shape and nothing else: how it went is the
// human's to read, which is the line a Shell was made to hold.
func TestWatch_AShellIsAskedItsShapeAndNothingElse(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	g, judge := composer(t, map[string]string{
		"render_kind": "table", "header_line": "0", "parse_kind": "columns",
		"body": "table", "summary": "none",
	})
	defer g.Watch(t.Context(), bus)()

	views, unsub := bus.Subscribe(event.Only(event.ViewReadyKind))
	defer unsub()

	shell := uuid.Must(uuid.NewV7())
	bus.Publish(event.ShellStarted{Shell: shell, Command: "cat stats.txt"})
	bus.Publish(event.ShellEnded{Shell: shell, Result: event.Result{Stdout: "pkg secs status\n" +
		"a 1.2 ok\nb 3.4 ok\nc 0.5 FAIL\nd 9.9 ok\ne 1.1 ok\nf 2.2 ok\ng 3.3 ok\nh 4.4 ok\n"}})

	got := viewFor(t, views, shell)
	assert.Equal(t, string(viewgen.SourceGenerated), got.Source)

	judge.mu.Lock()
	defer judge.mu.Unlock()
	require.NotEmpty(t, judge.asked)
	assert.Equal(t, []string{"render_kind"}, judge.asked[0])
	for _, batch := range judge.asked {
		assert.NotContains(t, batch, "result_status")
		assert.NotContains(t, batch, "goal_achieved")
	}
}

// A diff composes nothing, and a Shell's row has no kind for ui to fall
// back on, so the kind's own view is sent instead.
func TestWatch_AShellFallsBackToItsKindsView(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	g, _ := composer(t, map[string]string{"render_kind": viewgen.KindDiff})
	defer g.Watch(t.Context(), bus)()

	views, unsub := bus.Subscribe(event.Only(event.ViewReadyKind))
	defer unsub()

	shell := uuid.Must(uuid.NewV7())
	bus.Publish(event.ShellStarted{Shell: shell, Command: "git diff"})
	bus.Publish(event.ShellEnded{Shell: shell, Result: event.Result{Stdout: "diff --git a/x b/x\n" +
		"--- a/x\n+++ b/x\n@@ -1,4 +1,4 @@\n-one\n+uno\n two\n three\n-four\n+cuatro\n"}})

	got := viewFor(t, views, shell)
	require.Len(t, got.Spec.Blocks, 1)
	assert.Equal(t, "diff", got.Spec.Blocks[0].Kind)
}

// With no judge wired nothing publishes CallJudged, and a Call must not
// wait for one: the shipped view draws as soon as it ends.
func TestWatch_AnUnjudgedCallResolvesWhenItEnds(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	g := &viewgen.Generator{Registry: ui.Registry(), Unjudged: true}
	defer g.Watch(t.Context(), bus)()

	views, unsub := bus.Subscribe(event.Only(event.ViewReadyKind))
	defer unsub()

	call := uuid.Must(uuid.NewV7())
	bus.Publish(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1})
	bus.Publish(event.CallProposed{Call: call, Tool: "bash",
		Args: map[string]any{"command": "go test ./..."}})
	bus.Publish(event.CallEnded{Call: call, Result: event.Result{Stdout: goTest}})

	got := viewFor(t, views, call)
	assert.Equal(t, "go test", got.Spec.Match)
}

// Shells of one shipped command resolve at once, in parallel, and share
// nothing mutable with each other or with ui.
func TestWatch_ParallelShellsShareNoState(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	g := &viewgen.Generator{Registry: ui.Registry()}
	defer g.Watch(t.Context(), bus)()

	views, unsub := bus.Subscribe(event.Only(event.ViewReadyKind))
	defer unsub()

	const n = 6
	for range n {
		shell := uuid.Must(uuid.NewV7())
		bus.Publish(event.ShellStarted{Shell: shell, Command: "go test ./..."})
		bus.Publish(event.ShellEnded{Shell: shell, Result: event.Result{Stdout: goTest}})
	}
	for range n {
		select {
		case rec := <-views:
			got, ok := rec.Event.(event.ViewReady)
			require.True(t, ok)
			assert.Equal(t, "go test", got.Spec.Match)
		case <-time.After(3 * time.Second):
			t.Fatal("a view never arrived")
		}
	}
}

// Stopping cancels a composition waiting on the judge, rather than
// waiting out the client's timeout, and publishes nothing for it.
func TestWatch_StopCancelsCompositionInFlight(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	judge := &blockingJudge{asked: make(chan struct{}, 1)}
	g := &viewgen.Generator{Judge: judge, Registry: ui.Registry()}
	stop := g.Watch(t.Context(), bus)

	views, unsub := bus.Subscribe(event.Only(event.ViewReadyKind))
	defer unsub()

	shell := uuid.Must(uuid.NewV7())
	bus.Publish(event.ShellStarted{Shell: shell, Command: "cat stats.txt"})
	bus.Publish(event.ShellEnded{Shell: shell, Result: event.Result{Stdout: strings.Repeat("a 1 ok\n", 10)}})
	select {
	case <-judge.asked:
	case <-time.After(3 * time.Second):
		t.Fatal("the judge was never asked")
	}

	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("stop waited on a composition nobody wants any more")
	}
	select {
	case rec := <-views:
		t.Fatalf("a stopped composer published %s", rec.Event.Kind())
	case <-time.After(200 * time.Millisecond):
	}
}

// blockingJudge answers nothing until its ctx is cancelled.
// The session's ctx ending abandons a composition without anyone calling stop.
func TestWatch_ItsContextEndingCancelsCompositionInFlight(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	ctx, cancel := context.WithCancel(t.Context())
	judge := &blockingJudge{asked: make(chan struct{}, 1), quit: make(chan struct{})}
	g := &viewgen.Generator{Judge: judge, Registry: ui.Registry()}
	defer g.Watch(ctx, bus)()

	shell := uuid.Must(uuid.NewV7())
	bus.Publish(event.ShellStarted{Shell: shell, Command: "cat stats.txt"})
	bus.Publish(event.ShellEnded{Shell: shell, Result: event.Result{Stdout: strings.Repeat("a 1 ok\n", 10)}})
	select {
	case <-judge.asked:
	case <-time.After(3 * time.Second):
		t.Fatal("the judge was never asked")
	}
	cancel()
	select {
	case <-judge.quit:
	case <-time.After(3 * time.Second):
		t.Fatal("the composition did not give up when its ctx ended")
	}
}

// blockingJudge blocks until its ctx ends. quit, when set, is closed then.
type blockingJudge struct {
	asked, quit chan struct{}
	once        sync.Once
}

func (j *blockingJudge) Ask(ctx context.Context, _ classify.State, _ classify.Questions) (classify.Answers, classify.Usage, error) {
	j.asked <- struct{}{}
	<-ctx.Done()
	if j.quit != nil {
		j.once.Do(func() { close(j.quit) })
	}
	return nil, classify.Usage{}, ctx.Err()
}

func viewFor(t *testing.T, views <-chan event.Record, id uuid.UUID) event.ViewReady {
	t.Helper()
	select {
	case rec := <-views:
		got, ok := rec.Event.(event.ViewReady)
		require.True(t, ok)
		require.Equal(t, id, got.Call, "the view is for the Shell that ran")
		require.NotNil(t, got.Spec)
		return got
	case <-time.After(3 * time.Second):
		t.Fatal("no view arrived")
	}
	return event.ViewReady{}
}
