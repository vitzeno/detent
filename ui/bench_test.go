package ui

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/vitzeno/detent/event"
)

// session builds a history the size of a real one: 21 Turns, 82 Calls.
func session(turns, callsPerTurn, outLines int) Model {
	m := New(context.Background(), event.New(), SessionInfo{})
	m.layout.width, m.layout.height = 120, 40
	for i := range turns {
		turn := uuid.Must(uuid.NewV7())
		m.apply(event.TurnStarted{Turn: turn, N: i + 1, Prompt: fmt.Sprintf("request number %d about the codebase", i)})
		for range callsPerTurn {
			call, step := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			m.apply(event.CallProposed{Call: call, Step: step, Tool: "bash",
				Args: map[string]any{"command": "go test ./... -run TestSomething -v"}})
			m.apply(event.CallStarted{Call: call, Runner: "sandbox"})
			for l := range outLines {
				m.apply(event.OutputChunk{Call: call, Line: fmt.Sprintf("  ok   github.com/vitzeno/detent/internal/pkg%d  0.42s", l)})
			}
			m.apply(event.CallEnded{Call: call, Result: event.Result{Stdout: "ok\n"}})
		}
		m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone})
	}
	return m
}

func BenchmarkView(b *testing.B) {
	for _, c := range []struct {
		name              string
		turns, calls, out int
	}{
		{"small_1turn_2calls", 1, 2, 5},
		{"real_21turns_4calls", 21, 4, 20},
		{"heavy_50turns_8calls", 50, 8, 40},
	} {
		m := session(c.turns, c.calls, c.out)
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = m.View()
			}
		})
	}
}

// BenchmarkApplyOutputChunk is the hot path: one live line arriving.
func BenchmarkApplyOutputChunk(b *testing.B) {
	m := session(21, 4, 20)
	call := uuid.Must(uuid.NewV7())
	m.apply(event.CallProposed{Call: call, Tool: "bash", Args: map[string]any{"command": "make test"}})
	m.apply(event.CallStarted{Call: call, Runner: "sandbox"})
	b.ReportAllocs()
	for b.Loop() {
		m.apply(event.OutputChunk{Call: call, Line: "ok  github.com/vitzeno/detent/ui  0.42s"})
	}
}

// BenchmarkUpdateAndView is the real per-event cost: Update resizes
// and re-renders, then Bubble Tea calls View.
func BenchmarkUpdateAndView(b *testing.B) {
	for _, c := range []struct {
		name              string
		turns, calls, out int
	}{
		{"real_21turns_4calls", 21, 4, 20},
		{"heavy_50turns_8calls", 50, 8, 40},
	} {
		m := session(c.turns, c.calls, c.out)
		call := uuid.Must(uuid.NewV7())
		m.apply(event.CallProposed{Call: call, Tool: "bash", Args: map[string]any{"command": "make test"}})
		m.apply(event.CallStarted{Call: call, Runner: "sandbox"})
		msg := factMsg{[]event.Event{event.OutputChunk{Call: call, Line: "ok  detent/ui  0.42s"}}}

		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			cur := m
			for b.Loop() {
				next, _ := cur.Update(msg)
				cur = next.(Model)
				_ = cur.View()
			}
		})
	}
}
