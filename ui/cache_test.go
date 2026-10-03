package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// The one thing a cache can do wrong: after every mutation the UI
// supports, the cached drawing must equal a cold one.
func TestBlockCache_NeverGoesStale(t *testing.T) {
	m := session(6, 3, 8)
	rows := m.rows()
	turn := m.blocks[2].id
	call := m.blocks[2].rows[1].id

	steps := []struct {
		name string
		do   func(m *Model)
	}{
		{"cursor moves", func(m *Model) { m.nav.cursor = 4 }},
		{"cursor moves again", func(m *Model) { m.nav.cursor = 9 }},
		{"a row expands", func(m *Model) { m.toggleExpand(rows[4]) }},
		{"the same row shuts", func(m *Model) { m.toggleExpand(rows[4]) }},
		{"output arrives", func(m *Model) {
			m.apply(event.OutputChunk{ToolCall: call, Line: "a new line of output"})
		}},
		{"a verdict lands", func(m *Model) {
			m.apply(event.ToolCallJudged{ToolCall: call, Status: "failed", Attention: 0.95, FromJudge: true})
		}},
		{"a turn ends", func(m *Model) {
			m.apply(event.TurnEnded{Turn: turn, Reason: event.EndError, Summary: "it broke"})
		}},
		{"a checkpoint lands", func(m *Model) { m.apply(event.CheckpointTaken{Turn: turn}) }},
		{"the pane narrows", func(m *Model) { m.layout.histColW = 44 }},
		{"a new turn starts", func(m *Model) {
			m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 7, Prompt: "one more"})
		}},
		{"the model speaks", func(m *Model) {
			m.apply(event.ModelText{Turn: m.cur.id, Text: "looking into it"})
		}},
		{"a call is flagged", func(m *Model) {
			m.apply(event.ToolCallAssessed{ToolCall: call, Risk: event.Risk{Dangerous: true}})
		}},
		{"a sign-in is asked for", func(m *Model) {
			m.apply(event.AuthorizationWaiting{Server: "notion", URL: "https://x", Until: time.Now()})
		}},
		{"the sign-in lands", func(m *Model) { m.apply(event.ServerAuthorized{Server: "notion"}) }},
		{"the human runs a command", func(m *Model) {
			m.apply(event.UserCommandStarted{UserCommand: uuid.Must(uuid.NewV7()), Command: "ls"})
		}},
		{"the live turn ends", func(m *Model) {
			m.apply(event.TurnEnded{Turn: m.cur.id, Reason: event.EndAborted})
		}},
		{"a prompt is sent", func(m *Model) {
			*m, _ = m.sendPrompt("and again")
		}},
		{"an old turn is undone", func(m *Model) { m.apply(event.RolledBack{Turn: turn}) }},
	}

	for _, s := range steps {
		s.do(&m)
		warm, warmCursor := m.historyAll()
		cold, coldCursor := m.uncached().historyAll()
		require.Equal(t, cold, warm, "after %s: the cache drew something a cold render would not", s.name)
		require.Equal(t, coldCursor, warmCursor, "after %s: cursor line diverged", s.name)
	}
}

// The cache exists to be hit: correctness alone passes with it off.
func TestBlockCache_IsActuallyHit(t *testing.T) {
	m := session(40, 3, 8)
	m.nav.cursor = 60
	_, _ = m.historyAll()

	cached := 0
	for _, b := range m.blocks {
		if b.cache != nil {
			cached++
		}
	}
	require.Equal(t, len(m.blocks), cached, "every block should have been cached")

	// One row: only the blocks it left and joined may redraw.
	before := make([]*blockCache, len(m.blocks))
	for i, b := range m.blocks {
		before[i] = b.cache
	}
	m.nav.cursor = 61
	_, _ = m.historyAll()

	redrawn := 0
	for i, b := range m.blocks {
		if b.cache != before[i] {
			redrawn++
		}
	}
	assert.LessOrEqual(t, redrawn, 2, "a cursor move redrew %d blocks, not the two it touches", redrawn)
}

// Scrolled up, history is laid out whole, so a live line that redrew
// every block would make each frame cost what the session has done.
func TestBlockCache_ALiveLineRedrawsOnlyItsBlock(t *testing.T) {
	m := session(40, 3, 0)
	turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m.apply(event.TurnStarted{Turn: turn, N: 41, Prompt: "run something"})
	m.apply(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "make test"}})
	m.apply(event.ToolCallStarted{ToolCall: call, Runner: "sandbox"})
	m.nav.follow, m.nav.cursor = false, 5
	_, _ = m.historyAll()

	before := make([]*blockCache, len(m.blocks))
	for i, b := range m.blocks {
		before[i] = b.cache
	}
	m.apply(event.OutputChunk{ToolCall: call, Line: "ok"})
	m.apply(event.StepEnded{Turn: turn})
	_, _ = m.historyAll()

	for i, b := range m.blocks[:len(m.blocks)-1] {
		assert.Same(t, before[i], b.cache, "block %d redrew for a line that was not in it", i)
	}
}

// The live block redraws per frame, or "thinking…" freezes.
func TestBlockCache_TheSpinnerStillTurns(t *testing.T) {
	m := session(3, 2, 0)
	turn := uuid.Must(uuid.NewV7())
	m.apply(event.TurnStarted{Turn: turn, N: 4, Prompt: "still working"})
	require.True(t, m.waiting)

	before, _ := m.historyAll()
	m.spinner, _ = m.spinner.Update(m.spinner.Tick())
	after, _ := m.historyAll()

	assert.NotEqual(t, before, after, "the cache pinned the spinner to one frame")
}

// Every running row draws a spinner, not just the thinking line, so a
// block holding one has to redraw as the frame advances.
func TestBlockCache_ARunningToolCallKeepsSpinning(t *testing.T) {
	m := session(3, 2, 0)
	turn := uuid.Must(uuid.NewV7())
	call := uuid.Must(uuid.NewV7())
	m.apply(event.TurnStarted{Turn: turn, N: 4, Prompt: "run something"})
	m.apply(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "make test"}})
	m.apply(event.ToolCallStarted{ToolCall: call, Runner: "sandbox"})
	require.True(t, anyRunning(m.cur), "the test needs a running row")

	before, _ := m.historyAll()
	m.spinner, _ = m.spinner.Update(m.spinner.Tick())
	after, _ := m.historyAll()

	assert.NotEqual(t, before, after, "the running call's spinner is frozen by the cache")
}

// Equivalence over a fixed list cannot catch an input missing from both
// the key and the list, so each input must move the key on its own.
func TestBlockKey_CoversEverythingABlockDrawsFrom(t *testing.T) {
	inputs := []struct {
		name string
		do   func(m *Model, b *turnBlock)
	}{
		{"its content", func(m *Model, b *turnBlock) {
			m.apply(event.ToolCallJudged{ToolCall: b.rows[0].id, Status: "failed"})
		}},
		{"the pane width", func(m *Model, _ *turnBlock) { m.layout.histColW = 44 }},
		{"the cursor entering it", func(m *Model, _ *turnBlock) { m.nav.cursor = len(m.rows()) - 1 }},
		{"the spinner while live", func(m *Model, _ *turnBlock) {
			m.spinner, _ = m.spinner.Update(m.spinner.Tick())
		}},
	}
	for _, in := range inputs {
		t.Run(in.name, func(t *testing.T) {
			m := session(3, 2, 0)
			m.layout.histColW = 60
			turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			m.apply(event.TurnStarted{Turn: turn, N: 4, Prompt: "live"})
			m.apply(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "make"}})
			m.apply(event.ToolCallStarted{ToolCall: call})
			m.nav.cursor = 0
			b := m.cur
			before := m.blockKey(b, m.focusedRow())

			in.do(&m, b)
			assert.NotEqual(t, before, m.blockKey(b, m.focusedRow()),
				"%s changed what the block draws, but not the key it is cached under", in.name)
		})
	}
}

func TestHistKey_CoversEverythingHistoryAssemblesFrom(t *testing.T) {
	inputs := []struct {
		name string
		do   func(m *Model)
	}{
		{"a fact arrives", func(m *Model) { m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 9}) }},
		{"a row expands", func(m *Model) { m.toggleExpand(m.rows()[1]) }},
		{"the pane width", func(m *Model) { m.layout.histColW = 44 }},
		{"the cursor", func(m *Model) { m.nav.cursor = 2 }},
		{"the spinner while live", func(m *Model) {
			m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 9})
			_, _ = m.historyAll()
			m.spinner, _ = m.spinner.Update(m.spinner.Tick())
		}},
	}
	for _, in := range inputs {
		t.Run(in.name, func(t *testing.T) {
			m := session(3, 2, 0)
			m.layout.histColW = 60
			_, _ = m.historyAll()
			before := m.hist.key

			in.do(&m)
			_, _ = m.historyAll()
			assert.NotEqual(t, before, m.hist.key,
				"%s changed what history draws, but not the key it is cached under", in.name)
		})
	}
}

// The pane must never show content the current state would not
// produce. Columnar, since that is what it draws width-sensitively.
func TestDetailCache_NeverGoesStale(t *testing.T) {
	m := session(4, 3, 6)
	rows := m.rows()
	// Rows must draw differently, or a cursor move proves nothing.
	m.apply(event.ToolCallEnded{ToolCall: rows[2].id, Result: event.Result{Stdout: "row two is its own thing\n"}})

	// A fresh tool call: a row binds its view once, so a second result on
	// an existing row would leave it drawing the first.
	shown := uuid.Must(uuid.NewV7())
	m.apply(event.ToolCallProposed{ToolCall: shown, Tool: "bash", Args: map[string]any{"command": "df -h"}})
	m.nav.cursor = len(m.rows()) - 1
	m.layout.width, m.layout.height = 150, 45
	m.sizeViewport()

	var table strings.Builder
	table.WriteString("Filesystem      Size  Used Avail Capacity  Mounted on\n")
	for i := range 20 {
		fmt.Fprintf(&table, "/dev/disk%-3d    460Gi %dGi  %dGi    %d%%    /mnt/point%d\n", i, i*7, 400-i, i*3, i)
	}

	steps := []struct {
		name string
		do   func(m *Model)
	}{
		{"first render", func(*Model) {}},
		{"the shown row streams output", func(m *Model) {
			m.apply(event.ToolCallStarted{ToolCall: shown, Runner: "sandbox"})
			m.apply(event.OutputChunk{ToolCall: shown, Line: "a fresh line nobody has drawn yet"})
		}},
		{"the shown row finishes", func(m *Model) {
			m.apply(event.ToolCallEnded{ToolCall: shown, Result: event.Result{Stdout: table.String()}})
		}},
		{"the focused row changes", func(m *Model) { m.nav.cursor = 2 }},
		{"and changes back", func(m *Model) { m.nav.cursor = len(m.rows()) - 1 }},
		{"the shown row expands", func(m *Model) { m.toggleExpand(m.focused()) }},
		{"usage opens", func(m *Model) { m.panel.open = panelContext }},
		{"help opens", func(m *Model) { m.panel.open = panelHelp }},
		{"the panel closes", func(m *Model) { m.panel.open = panelNone }},
		// Via layout.width: sizeViewport derives outputColW from it.
		{"the terminal narrows", func(m *Model) { m.layout.width = 74; m.sizeViewport() }},
		{"the terminal widens", func(m *Model) { m.layout.width = 190; m.sizeViewport() }},
		{"the terminal shortens", func(m *Model) { m.layout.height = 20; m.sizeViewport() }},
		{"focus moves to the pane", func(m *Model) { m.nav.focus = focusOutput }},
		{"the table cursor moves", func(m *Model) {
			if r := m.focused(); r != nil {
				r.tableCursor = 4
			}
		}},
		{"undo opens", func(m *Model) { m.mode = modeUndo }},
		{"undo closes", func(m *Model) { m.mode = modeInput }},
	}

	for _, s := range steps {
		s.do(&m)
		m.refreshViewport()
		require.Equal(t, m.coldDetail(), m.viewContent,
			"after %s: the pane kept content a fresh draw would not produce", s.name)
	}
}

// Scrolling must not redraw, or the skip is not doing its job.
func TestDetailCache_ScrollingDoesNotRedraw(t *testing.T) {
	m := session(4, 3, 6)
	m.nav.focus = focusOutput
	m.sizeViewport()
	m.refreshViewport()
	was := m.detail

	got, _ := m.update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, was, got.detail, "a scroll changed the key, so the content was redrawn")
}

// The skip is only as safe as the key is complete, and comparing
// rendered content misses fields, since two inputs often draw alike.
func TestDetailKey_CoversEverythingThePaneDrawsFrom(t *testing.T) {
	inputs := []struct {
		name string
		do   func(m *Model)
	}{
		{"a fact arrives", func(m *Model) { m.apply(event.StepStarted{Turn: m.blocks[0].id, Step: uuid.Must(uuid.NewV7()), N: 9}) }},
		{"the terminal width", func(m *Model) { m.layout.width = 74; m.sizeViewport() }},
		{"the terminal height", func(m *Model) { m.layout.height = 20; m.sizeViewport() }},
		{"the focused row", func(m *Model) { m.nav.cursor = 1 }},
		{"a panel opens", func(m *Model) { m.panel.open = panelContext }},
		{"the mode", func(m *Model) { m.mode = modeUndo }},
		{"which pane has focus", func(m *Model) { m.nav.focus = focusOutput }},
		{"the table cursor", func(m *Model) { m.focused().tableCursor = 4 }},
	}

	for _, in := range inputs {
		t.Run(in.name, func(t *testing.T) {
			m := session(3, 3, 4)
			m.nav.cursor, m.nav.focus = 2, focusHistory
			m.layout.width, m.layout.height = 150, 45
			m.sizeViewport()
			before := m.detailKey()

			in.do(&m)
			assert.NotEqual(t, before, m.detailKey(),
				"%s changed what the pane draws, but not the key it is cached under", in.name)
		})
	}
}

// uncached drops every block cache, for comparing warm against cold.
func (m Model) uncached() Model {
	for _, b := range m.blocks {
		b.cache = nil
	}
	return m
}

// coldDetail recomputes the pane, for comparing against the skip.
func (m Model) coldDetail() string {
	m.detail = detailKey{}
	m.refreshViewport()
	return m.viewContent
}
