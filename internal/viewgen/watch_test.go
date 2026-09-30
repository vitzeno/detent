package viewgen_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/viewgen"
	"github.com/vitzeno/detent/ui"
)

// A view is keyed by what bash ran, not by bash: keyed on the tool,
// no shipped spec could ever match and every command shared one.
func TestWatch_KeysOnTheCommandNotTheTool(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	g := &viewgen.Generator{Registry: ui.Registry()}
	defer g.Watch(bus)()

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
		got := rec.Event.(event.ViewReady)
		require.NotNil(t, got.Spec)
		assert.Equal(t, "go test", got.Spec.Match)
		assert.Equal(t, string(viewgen.SourceShipped), got.Source)
	case <-time.After(3 * time.Second):
		t.Fatal("the shipped go test view never arrived")
	}
}

// A command the human ran was never judged, so nothing drew it: not
// even go test, which detent ships a view for.
func TestWatch_AShellGetsTheShippedViewWithoutAJudge(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	g := &viewgen.Generator{Registry: ui.Registry()}
	defer g.Watch(bus)()

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
	defer g.Watch(bus)()

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
	defer g.Watch(bus)()

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

func viewFor(t *testing.T, views <-chan event.Record, id uuid.UUID) event.ViewReady {
	t.Helper()
	select {
	case rec := <-views:
		got := rec.Event.(event.ViewReady)
		require.Equal(t, id, got.Call, "the view is for the Shell that ran")
		require.NotNil(t, got.Spec)
		return got
	case <-time.After(3 * time.Second):
		t.Fatal("no view arrived")
	}
	return event.ViewReady{}
}
