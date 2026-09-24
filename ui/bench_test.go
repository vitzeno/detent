package ui

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/vitzeno/detent/event"
)

// stressSizes are multiples of one real session measured from the
// logs: 21 Turns, 84 Calls, 871 events.
var stressSizes = []struct {
	name         string
	turns, calls int
}{
	{"1x_21turns", 21, 4},
	{"10x_210turns", 210, 4},
	{"20x_420turns", 420, 4},
	{"50x_1050turns", 1050, 4},
}

func session(turns, callsPerTurn, outLines int) Model {
	m := New(context.Background(), event.New(), SessionInfo{})
	m.layout.width, m.layout.height = 150, 45
	m.nav.histHeight, m.nav.follow = 30, true
	for i := range turns {
		turn := uuid.Must(uuid.NewV7())
		m.apply(event.TurnStarted{Turn: turn, N: i + 1, Prompt: fmt.Sprintf("request %d about the codebase", i)})
		for range callsPerTurn {
			call, step := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			m.apply(event.CallProposed{Call: call, Step: step, Tool: "bash",
				Args: map[string]any{"command": "go test ./... -run TestSomething -v"}})
			m.apply(event.CallStarted{Call: call, Runner: "sandbox"})
			for l := range outLines {
				m.apply(event.OutputChunk{Call: call, Line: fmt.Sprintf("  ok   detent/internal/pkg%d  0.42s", l)})
			}
			m.apply(event.CallEnded{Call: call, Result: event.Result{Stdout: "ok\n"}})
		}
		m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone})
	}
	return m
}

// BenchmarkStressPerEvent is what a streaming Call costs per line at
// each session size: one Update, then the View Bubble Tea asks for.
func BenchmarkStressPerEvent(b *testing.B) {
	for _, s := range stressSizes {
		m := session(s.turns, s.calls, 20)
		call := uuid.Must(uuid.NewV7())
		m.apply(event.CallProposed{Call: call, Tool: "bash", Args: map[string]any{"command": "make test"}})
		m.apply(event.CallStarted{Call: call, Runner: "sandbox"})
		msg := oneChunk(call)
		b.Run(s.name, func(b *testing.B) {
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

// BenchmarkStressBurst is the case that felt worst: 200 lines landing
// at once, which is what one noisy command produces.
func BenchmarkStressBurst(b *testing.B) {
	for _, s := range stressSizes {
		m := session(s.turns, s.calls, 20)
		call := uuid.Must(uuid.NewV7())
		m.apply(event.CallProposed{Call: call, Tool: "bash", Args: map[string]any{"command": "make test"}})
		m.apply(event.CallStarted{Call: call, Runner: "sandbox"})
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			cur := m
			for b.Loop() {
				cur = burst(cur, call, 200)
			}
		})
	}
}

func oneChunk(call uuid.UUID) factMsg {
	return factMsg{[]event.Event{chunk(call)}}
}

// burst feeds lines the way the real pump does: coalesced into
// batches, one render each.
func burst(m Model, call uuid.UUID, n int) Model {
	for i := 0; i < n; i += maxFactBatch {
		batch := make([]event.Event, 0, maxFactBatch)
		for j := i; j < min(i+maxFactBatch, n); j++ {
			batch = append(batch, chunk(call))
		}
		next, _ := m.Update(factMsg{batch})
		m = next.(Model)
		_ = m.View()
	}
	return m
}

func chunk(call uuid.UUID) event.Event {
	return event.OutputChunk{Call: call, Line: "ok   detent/ui   0.42s"}
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
