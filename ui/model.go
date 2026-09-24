package ui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/event"
)

// Model is a projection of the event stream: apply.go folds facts in,
// and every key publishes an intent.
type Model struct {
	bus *event.Bus
	ctx context.Context

	info SessionInfo

	prompt  prompt
	output  viewport.Model
	spinner spinner.Model

	blocks []*turnBlock
	cur    *turnBlock

	mode    mode
	waiting bool

	// asking and bound are the two questions the engine can put to a
	// human. Both are answered by publishing, never by calling.
	asking *event.ApprovalAsked
	bound  *event.BoundReached

	nav    navState
	panel  panelState
	layout layoutState
	notice noticeState
	undo   undoState

	// Counters for /usage and /status, folded from the stream rather
	// than read back from anywhere.
	calls, steps, errors, views, tokens int

	// run is how this session described itself at startup, and
	// sessions is what /sessions last heard back.
	run      event.SessionStarted
	sessions []event.SessionSummary

	welcomeFrame int
	viewContent  string

	// facts is the subscription. Re-armed by nextFact after each one,
	// which is what keeps ordering without a second goroutine.
	facts <-chan event.Record
}

// SessionInfo is what only the wiring knows. What SessionStarted
// carries is read off that, so a run has one description.
type SessionInfo struct {
	// Sandbox detail for the welcome pane; empty in host mode.
	Image   string
	Mount   string
	Runtime string
	Network string
}

// New builds the TUI over bus. It subscribes immediately, so nothing
// published between here and the first Update is lost.
func New(ctx context.Context, bus *event.Bus, info SessionInfo) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(accent)

	facts, _ := bus.Subscribe(event.Facts())
	return Model{
		bus: bus, ctx: ctx, info: info,
		prompt:  newPrompt(),
		output:  viewport.New(),
		spinner: sp,
		nav:     navState{follow: true},
		facts:   facts,
	}
}

func (m Model) Init() tea.Cmd {
	// Asked at startup so the welcome pane can say what is resumable,
	// and /sessions has an answer before it is opened.
	return tea.Batch(textarea.Blink, welcomeTick(), nextFact(m.facts),
		m.send(event.ListSessions{}))
}

// Update routes the message, then re-syncs the panes once, so no
// handler has to remember to resize or re-render.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.route(msg)
	updated, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	updated.sizeViewport()
	return updated, cmd
}

func (m Model) route(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.layout.width, m.layout.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.PasteMsg:
		return m.handlePaste(msg.Content)

	case tea.KeyboardEnhancementsMsg:
		m.prompt.SetRichKeys(msg.SupportsKeyDisambiguation())
		return m, nil

	case spinner.TickMsg:
		if !m.waiting {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	// Eight message types and seven command constructors collapsed to
	// one of each: the UI learns everything the same way.
	case factMsg:
		waiting := m.waiting
		for _, e := range msg.events {
			m.apply(e)
		}
		var cmd tea.Cmd
		// Only on the edge: Tick carries the live tag, so re-arming on
		// every fact restarts the chain and renders a frame to do it.
		if m.waiting && !waiting {
			cmd = m.spinner.Tick
		}
		return m, tea.Batch(cmd, nextFact(m.facts))

	case welcomeTickMsg:
		if !m.showWelcome() {
			return m, nil
		}
		m.welcomeFrame++
		return m, welcomeTick()
	}

	var cmd tea.Cmd
	m.output, cmd = m.output.Update(msg)
	return m, cmd
}

// send publishes an intent. Every key that changes what the engine is
// doing goes through here, and nothing else reaches it.
func (m Model) send(ev event.Event) tea.Cmd {
	return func() tea.Msg {
		m.bus.Publish(ev)
		return nil
	}
}

// factMsg carries every fact that was ready, in order.
type factMsg struct{ events []event.Event }

type welcomeTickMsg struct{}

// welcomeTick advances the boot pane's animation, and only while that
// pane is what is on screen.
func welcomeTick() tea.Cmd {
	return tea.Tick(welcomeFrameEvery, func(time.Time) tea.Msg { return welcomeTickMsg{} })
}

const welcomeFrameEvery = 90 * time.Millisecond

// maxFactBatch bounds one batch so a loud Call cannot starve keys.
const maxFactBatch = 256

// coalesceWindow is how long a batch gathers. The bus hands over one
// record at a time by design, so a burst needs a window to collect in;
// 2ms is well under a frame and far over a channel hop. Var for tests.
var coalesceWindow = 2 * time.Millisecond

// nextFact waits for one fact, then gathers whatever follows it
// closely. Output arrives a line at a time, so coalescing is what
// keeps a 500 line burst to a couple of renders instead of 500.
func nextFact(facts <-chan event.Record) tea.Cmd {
	return func() tea.Msg {
		rec, ok := <-facts
		if !ok {
			return nil
		}
		batch := []event.Event{rec.Event}
		window := time.NewTimer(coalesceWindow)
		defer window.Stop()
		for len(batch) < maxFactBatch {
			select {
			case rec, ok := <-facts:
				if !ok {
					return factMsg{batch}
				}
				batch = append(batch, rec.Event)
			case <-window.C:
				return factMsg{batch}
			}
		}
		return factMsg{batch}
	}
}

// rows flattens every block's calls into the one list the cursor
// indexes into.
func (m Model) rows() []*callRow {
	var out []*callRow
	for _, b := range m.blocks {
		out = append(out, b.rows...)
	}
	return out
}

func (m Model) focused() *callRow {
	rows := m.rows()
	if m.nav.cursor < 0 || m.nav.cursor >= len(rows) {
		return nil
	}
	return rows[m.nav.cursor]
}

// blockOf finds which block a row index falls in, for the rail.
func (m Model) blockOf(i int) *turnBlock {
	n := 0
	for _, b := range m.blocks {
		if i < n+len(b.rows) {
			return b
		}
		n += len(b.rows)
	}
	return nil
}

// runMode is how the session bar names where commands go.
func (m Model) runMode() string {
	if m.run.Sandbox {
		return "sandbox"
	}
	return "host"
}

func (m *Model) backToInput() {
	m.mode = modeInput
	m.nav.focus = focusInput
	m.prompt.Focus()
}

// trackNewest scrolls history whatever the human was doing, but moves
// the cursor only when nobody is reading the output pane.
func (m *Model) trackNewest() {
	m.nav.follow = true
	if m.nav.focus == focusOutput {
		return
	}
	m.nav.cursor = len(m.rows()) - 1
}

// Idle and RowCount expose just enough for a wiring test in
// cmd/detent, which cannot reach unexported state.
func (m Model) Idle() bool    { return m.cur == nil && !m.waiting }
func (m Model) RowCount() int { return len(m.rows()) }
