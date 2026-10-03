package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// stressSizes are multiples of one real session measured from the
// logs: 21 Turns, 84 tool calls, 871 events.
var stressSizes = []struct {
	name         string
	turns, calls int
}{
	{"1x_21turns", 21, 4},
	{"10x_210turns", 210, 4},
	{"20x_420turns", 420, 4},
	{"50x_1050turns", 1050, 4},
	{"100x_2100turns", 2100, 4},
}

// BenchmarkStressPerEvent is one output line: an Update and its View.
func BenchmarkStressPerEvent(b *testing.B) {
	for _, s := range stressSizes {
		m := session(s.turns, s.calls, 20)
		call := uuid.Must(uuid.NewV7())
		m.apply(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "make test"}})
		m.apply(event.ToolCallStarted{ToolCall: call, Runner: "sandbox"})
		msg := oneChunk(call)
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			cur := m
			for b.Loop() {
				cur, _ = cur.update(msg)
				_ = cur.View()
			}
		})
	}
}

// BenchmarkStressPerEventScrolledUp is the same line read from the top
// of history, where the tail shortcut does not apply.
func BenchmarkStressPerEventScrolledUp(b *testing.B) {
	for _, s := range stressSizes {
		b.Run(s.name, func(b *testing.B) {
			m := session(s.turns, s.calls, 20)
			call := uuid.Must(uuid.NewV7())
			m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: s.turns + 1, Prompt: "live"})
			m.apply(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "make test"}})
			m.apply(event.ToolCallStarted{ToolCall: call, Runner: "sandbox"})
			m.nav.focus, m.nav.follow, m.nav.cursor = focusHistory, false, 3
			m.sizeViewport()
			msg := oneChunk(call)
			b.ReportAllocs()
			cur := m
			for b.Loop() {
				cur, _ = cur.update(msg)
				_ = cur.View()
			}
		})
	}
}

// BenchmarkStressBurst is 200 lines at once, one noisy command.
func BenchmarkStressBurst(b *testing.B) {
	for _, s := range stressSizes {
		m := session(s.turns, s.calls, 20)
		call := uuid.Must(uuid.NewV7())
		m.apply(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "make test"}})
		m.apply(event.ToolCallStarted{ToolCall: call, Runner: "sandbox"})
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			cur := m
			for b.Loop() {
				cur = burst(cur, call, 200)
			}
		})
	}
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
	m.apply(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "make test"}})
	m.apply(event.ToolCallStarted{ToolCall: call, Runner: "sandbox"})
	b.ReportAllocs()
	for b.Loop() {
		m.apply(event.OutputChunk{ToolCall: call, Line: "ok  github.com/vitzeno/detent/ui  0.42s"})
	}
}

// BenchmarkUpdateAndView is the real per-event cost, Update plus View.
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
		m.apply(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "make test"}})
		m.apply(event.ToolCallStarted{ToolCall: call, Runner: "sandbox"})
		msg := factMsg{[]event.Event{event.OutputChunk{ToolCall: call, Line: "ok  detent/ui  0.42s"}}}

		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			cur := m
			for b.Loop() {
				cur, _ = cur.update(msg)
				_ = cur.View()
			}
		})
	}
}

// BenchmarkScrollUp is a trackpad scrolling back: in alt screen one
// flick arrives as a burst of arrow keys, each off the tail's fast path.
func BenchmarkScrollUp(b *testing.B) {
	up := tea.KeyPressMsg{Code: tea.KeyUp}
	for _, s := range stressSizes {
		m := session(s.turns, s.calls, 20)
		m.nav.focus = focusHistory
		m.nav.cursor = len(m.rows()) - 1
		m.sizeViewport()

		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			cur := m
			for b.Loop() {
				if cur.nav.cursor == 0 {
					cur.nav.cursor = len(cur.rows()) - 1
					cur.nav.follow = false
				}
				cur, _ = cur.update(up)
				_ = cur.View()
			}
		})
	}
}

// BenchmarkScrollFollowing is the same keystroke still at the tail.
func BenchmarkScrollFollowing(b *testing.B) {
	down := tea.KeyPressMsg{Code: tea.KeyDown}
	for _, s := range stressSizes {
		m := session(s.turns, s.calls, 20)
		m.nav.focus = focusHistory
		m.nav.cursor = len(m.rows()) - 1
		m.nav.follow = true
		m.sizeViewport()

		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			cur := m
			for b.Loop() {
				cur, _ = cur.update(down)
				_ = cur.View()
			}
		})
	}
}

// BenchmarkScrollOutputPane is scrolling the detail pane.
func BenchmarkScrollOutputPane(b *testing.B) {
	down := tea.KeyPressMsg{Code: tea.KeyDown}
	for _, s := range stressSizes {
		m := withBigOutput(session(s.turns, s.calls, 20), 2000)
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			cur := m
			for b.Loop() {
				cur, _ = cur.update(down)
				_ = cur.View()
			}
		})
	}
}

// BenchmarkScrollContextPanel is /context, whose history grows with the session.
func BenchmarkScrollContextPanel(b *testing.B) {
	down := tea.KeyPressMsg{Code: tea.KeyDown}
	for _, s := range stressSizes {
		m := session(s.turns, s.calls, 20)
		m.panel.open = panelContext
		m.nav.focus = focusOutput
		m.sizeViewport()
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			cur := m
			for b.Loop() {
				cur, _ = cur.update(down)
				_ = cur.View()
			}
		})
	}
}

// session builds a finished history of turns, each with its calls run.
func session(turns, callsPerTurn, outLines int) Model {
	m := New(context.Background(), event.New(), SessionInfo{})
	m.layout.width, m.layout.height = 150, 45
	m.nav.histHeight, m.nav.follow = 30, true
	for i := range turns {
		turn := uuid.Must(uuid.NewV7())
		m.apply(event.TurnStarted{Turn: turn, N: i + 1, Prompt: fmt.Sprintf("request %d about the codebase", i)})
		for range callsPerTurn {
			call, step := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			m.apply(event.ToolCallProposed{ToolCall: call, Step: step, Tool: "bash",
				Args: map[string]any{"command": "go test ./... -run TestSomething -v"}})
			m.apply(event.ToolCallStarted{ToolCall: call, Runner: "sandbox"})
			for l := range outLines {
				m.apply(event.OutputChunk{ToolCall: call, Line: fmt.Sprintf("  ok   detent/internal/pkg%d  0.42s", l)})
			}
			m.apply(event.ToolCallEnded{ToolCall: call, Result: event.Result{Stdout: "ok\n"}})
		}
		m.apply(event.TurnEnded{Turn: turn, Reason: event.EndDone})
	}
	return m
}

func oneChunk(call uuid.UUID) factMsg {
	return factMsg{[]event.Event{chunk(call)}}
}

// burst feeds lines the way the pump does, coalesced into batches.
func burst(m Model, call uuid.UUID, n int) Model {
	for i := 0; i < n; i += maxFactBatch {
		batch := make([]event.Event, 0, maxFactBatch)
		for j := i; j < min(i+maxFactBatch, n); j++ {
			batch = append(batch, chunk(call))
		}
		m, _ = m.update(factMsg{batch})
		_ = m.View()
	}
	return m
}

func chunk(call uuid.UUID) event.Event {
	return event.OutputChunk{ToolCall: call, Line: "ok   detent/ui   0.42s"}
}

// withBigOutput gives the newest row a result worth scrolling.
func withBigOutput(m Model, lines int) Model {
	rows := m.rows()
	r := rows[len(rows)-1]
	var b strings.Builder
	for i := range lines {
		fmt.Fprintf(&b, "%-6d detent/internal/pkg%-3d  ok  0.42s  %d allocs\n", i, i%40, i*7)
	}
	r.result = &event.Result{Stdout: b.String()}
	r.running, r.viewTried, r.view = false, false, nil
	m.nav.cursor = len(rows) - 1
	m.nav.focus = focusOutput
	m.histRev++
	m.sizeViewport()
	return m
}

// The finder rebuilds its hits on every key, so this is a keystroke on a
// long session whose every output is at the 8KB cap.
func BenchmarkFinder_Keystroke(b *testing.B) {
	m := session(200, 5, 0)
	out := strings.Repeat("  ok   detent/internal/pkg  0.42s  PASS coverage: 81.2% of statements\n", 8*1024/70)
	for _, r := range m.rows() {
		r.result = &event.Result{Stdout: out}
	}
	for _, q := range []string{"t", "go test", "coverage 99"} {
		b.Run(q, func(b *testing.B) {
			for b.Loop() {
				_ = m.finderHits(q, finderAll)
			}
		})
	}
}
