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

// Model is a projection of the event stream: facts.go folds facts in,
// and every key publishes an intent.
type Model struct {
	bus *event.Bus
	ctx context.Context

	info SessionInfo

	prompt  prompt
	output  viewport.Model
	spinner spinner.Model

	blocks []*turnBlock
	// hist is the assembled history, behind a pointer so the copy of
	// Model that View works on can still fill it.
	hist *histCache
	// histRev moves whenever block content does, and every block cache
	// is keyed on it. Bumped by apply and toggleExpand.
	histRev int
	// detail is the key the output pane's content was last drawn for.
	detail detailKey
	cur    *turnBlock

	mode    mode
	entry   entry
	waiting bool

	// asking and bound are the two questions the engine can put to a
	// human. Both are answered by publishing, never by calling.
	asking *event.ApprovalAsked
	bound  *event.BoundReached

	nav    navState
	panel  panelState
	layout layoutState
	notice noticeState
	// context is the last Step's prompt tokens, which is the whole
	// transcript resent, so it is how full the budget is right now.
	context int
	// replaying suppresses the flashes, because a replayed fact is
	// history and a notice says something just happened.
	replaying bool
	// quitArmed is a quit asked for once while a request was running.
	quitArmed bool
	undo      undoState
	forget    forgetState

	// Counters for /usage and /status, folded from the stream rather
	// than read back from anywhere.
	calls, steps, errors, views, tokens int

	// run is how this session described itself at startup, and
	// sessions is what /sessions last heard back.
	run event.SessionStarted
	// skillCmds are /<name> for the skills run names.
	skillCmds []slashCmd
	sessions  []event.SessionSummary
	servers   []event.ServerSummary

	welcomeFrame int
	viewContent  string

	// facts is the subscription. Re-armed by nextFact after each one,
	// which is what keeps ordering without a second goroutine.
	facts <-chan event.Record
}

// SessionInfo is what only the wiring knows. What SessionStarted
// carries is read off that, so a run has one description.
type SessionInfo struct {
	// Sandbox detail for the welcome pane, empty in host mode.
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
		hist:    &histCache{},
	}
}

// Init starts the fact pump, the welcome animation and a session listing.
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

// Idle reports whether no request is open or awaited. It and RowCount
// exist for wiring tests outside ui, which cannot reach unexported state.
func (m Model) Idle() bool { return m.cur == nil && !m.waiting }

// RowCount is how many rows history holds.
func (m Model) RowCount() int { return len(m.rows()) }

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
		if !m.spinning() {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	// One message type for every fact: the UI learns everything the same way.
	case factMsg:
		spinning := m.spinning()
		for _, e := range msg.events {
			m.apply(e)
		}
		var cmd tea.Cmd
		// Edge only: Tick carries the live tag, so re-arming restarts
		// the chain and costs a frame.
		if m.spinning() && !spinning {
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
// record at a time, so a burst needs a window to collect in.
var coalesceWindow = 2 * time.Millisecond

// nextFact waits for one fact, then gathers whatever follows closely.
// Output is one event per line, so a burst becomes one render.
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
	n := 0
	for _, b := range m.blocks {
		n += len(b.rows)
	}
	out := make([]*callRow, 0, n)
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

// spinning reports whether anything on screen is still turning: the
// model thinking, a Call running, or a command the human ran.
func (m Model) spinning() bool {
	if m.waiting {
		return true
	}
	for _, b := range m.blocks {
		if anyRunning(b) {
			return true
		}
	}
	return false
}

// shellRunning is whether esc has a command of the human's to stop.
func (m Model) shellRunning() bool {
	for _, b := range m.blocks {
		for _, r := range b.rows {
			if r.human && r.running {
				return true
			}
		}
	}
	return false
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
