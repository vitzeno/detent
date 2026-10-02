package main

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/assert"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/engine"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
	"github.com/vitzeno/detent/ui"
)

// The one seam no package can test alone: ui publishes intents and the
// engine answers with facts over the bus main.go wires.
func TestWiring_TypingReachesTheEngineAndComesBack(t *testing.T) {
	bus := event.New()
	eng := engine.New(bus, &stubModel{replies: []model.Reply{{
		Text:  "looking",
		Calls: []event.ToolCall{{ID: "c1", Name: "bash", Args: map[string]any{"command": "ls"}}},
	}}}, tool.Standard(), stubSelector{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go eng.Run(ctx)

	m := ui.New(ctx, bus, ui.SessionInfo{})
	var cmds []tea.Cmd
	cmds = append(cmds, m.Init())

	// Type, then enter, exactly as a keyboard would.
	for _, r := range "list things" {
		next, cmd := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = next.(ui.Model)
		cmds = append(cmds, cmd)
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(ui.Model)
	cmds = append(cmds, cmd)

	m = pump(t, m, tea.Batch(cmds...), func(m ui.Model) bool { return m.Idle() && m.RowCount() > 0 })

	assert.True(t, m.Idle(), "the request must finish")
	// Three Steps of prose, the finishing check's second answer being the
	// last, plus the one call. bash is not read-only, so the check runs.
	assert.Equal(t, 4, m.RowCount())
}

// pump is a minimal Bubble Tea runtime: it runs every command in its
// own goroutine and feeds the messages back, never dropping one.
func pump(t *testing.T, m ui.Model, first tea.Cmd, until func(ui.Model) bool) ui.Model {
	t.Helper()
	msgs := make(chan tea.Msg, 256)
	run := func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			if msg := c(); msg != nil {
				msgs <- msg
			}
		}()
	}
	run(first)

	deadline := time.After(10 * time.Second)
	for !until(m) {
		select {
		case msg := <-msgs:
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, c := range batch {
					run(c)
				}
				continue
			}
			next, cmd := m.Update(msg)
			m = next.(ui.Model)
			run(cmd)
		case <-deadline:
			return m
		}
	}
	return m
}

type stubModel struct{ replies []model.Reply }

func (s *stubModel) Complete(context.Context, []event.Message, []map[string]any) (model.Reply, event.Usage, error) {
	if len(s.replies) == 0 {
		return model.Reply{Text: "done", Stop: "stop"}, event.Usage{}, nil
	}
	r := s.replies[0]
	s.replies = s.replies[1:]
	return r, event.Usage{PromptTokens: 1}, nil
}

type stubRunner struct{}

func (stubRunner) Run(_ context.Context, cmd string, lines chan<- capture.StreamEvent) (capture.Result, error) {
	if lines != nil {
		close(lines)
	}
	return capture.Result{Stdout: "ran " + cmd + "\n"}, nil
}

type stubSelector struct{}

func (stubSelector) Select(event.Risk) (engine.Runner, string) { return stubRunner{}, "host" }
