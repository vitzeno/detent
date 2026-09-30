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
