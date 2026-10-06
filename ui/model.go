package ui

import (
	"context"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"charm.land/lipgloss/v2"
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

const welcomeFrameEvery = 90 * time.Millisecond

// maxFactBatch bounds one batch so a loud tool call cannot starve keys.
const maxFactBatch = 256

// questionSettle is how long a question is on screen before a key answers it.
var questionSettle = 400 * time.Millisecond

// coalesceWindow is how long a batch gathers. The bus hands over one
// record at a time, so a burst needs a window to collect in.
var coalesceWindow = 2 * time.Millisecond

// Model is a projection of the event stream: facts.go folds facts in,
// and every key publishes an intent.
type Model struct { //nolint:recvcheck // Bubble Tea updates by value, while mutators and the page drawers take a pointer
	bus *event.Bus

	info SessionInfo

	prompt  prompt
	output  viewport.Model
	spinner spinner.Model
	// pulse counts spinner ticks, the clock a reviewer's glyph breathes by.
	pulse int

	blocks []*turnBlock
	// agents are the subagents history's spawn rows draw, by id, and
	// agentOrder the same in the order they started.
	agents     map[uuid.UUID]*agentState
	agentOrder []*agentState
	insp       inspectorState
	// hist is the assembled history, behind a pointer so the copy of
	// Model that View works on can still fill it.
	hist *histCache
	// histRev moves whenever history may assemble differently, and the
	// assembled cache is keyed on it. Each block keeps its own rev.
	histRev int
	// detail is the key the output pane's content was last drawn for.
	detail detailKey
	cur    *turnBlock

	mode    mode
	waiting bool

	// asked and bound are the two questions the engine can put to a
	// human. Both are answered by publishing, never by calling. Approvals
	// queue in the order raised, and the box shows the first.
	asked []event.ApprovalAsked
	// childAsks are subagents' questions, answered in the inspector, so one
	// never takes the keys from whatever the human is doing.
	childAsks []event.ApprovalAsked
	bound     *event.BoundReached
	confirm   confirmState
	// askedAt is when the current question went up, for settling.
	askedAt time.Time

	nav    navState
	panel  panelState
	layout layoutState
	notice noticeState
	// ctxTokens is the last Step's prompt tokens, which is the whole
	// transcript resent, so it is how full the budget is right now.
	ctxTokens int
	// replaying suppresses the flashes, because a replayed fact is
	// history and a notice says something just happened.
	replaying bool
	// quitArmed is a quit asked for once while a request was running, and
	// stopArmed a subagent the human asked once to stop.
	quitArmed bool
	stopArmed uuid.UUID
	undo      undoState
	forget    forgetState
	finder    finderState
	resume    resumeState
	review    reviewState
	reviews   []*reviewRecord
	// resuming is the session the picker asked for, replayed once it starts.
	resuming *sessionPreview

	// Counters for /context and /status, folded from the stream rather
	// than read back from anywhere.
	calls, steps, errors, views, tokens int

	// run is how this session described itself at startup.
	run event.SessionStarted
	// skillCmds are /<name> for the skills run names.
	skillCmds []slashCmd
	// measured is the newest breakdown of what the model is sent.
	measured event.ContextMeasured
	// sessions is what /sessions last heard back.
	sessions []event.SessionSummary
	servers  []event.ServerSummary

	welcomeFrame int
	viewContent  string
	// workDir is where this process runs, read once for the welcome pane.
	workDir string

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
	// PowerShell is true when the human's commands run in pwsh.
	PowerShell bool
}

// New builds the TUI over bus. It subscribes immediately, so nothing
// published between here and the first Update is lost, until ctx ends.
func New(ctx context.Context, bus *event.Bus, info SessionInfo) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(palette.Accent)

	facts, stop := bus.Subscribe(event.Facts())
	context.AfterFunc(ctx, stop)
	wd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	return Model{
		bus: bus, info: info, workDir: tildePath(wd, home),
		prompt:  newPrompt(info.PowerShell),
		output:  viewport.New(),
		spinner: sp,
		nav:     navState{follow: true},
		facts:   facts,
		hist:    &histCache{},
		agents:  map[uuid.UUID]*agentState{},
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
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) { return m.update(msg) }

// Idle reports whether no request is open or awaited. It and RowCount
// exist for wiring tests outside ui, which cannot reach unexported state.
func (m Model) Idle() bool { return m.cur == nil && !m.waiting }

// RowCount is how many rows history holds.
func (m Model) RowCount() int { return len(m.rows()) }

// update is Update for callers that want the Model back, not an interface.
func (m Model) update(msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.route(msg)
	// A key may have emptied the bar a question was waiting behind.
	next.raiseWaiting()
	next.sizeViewport()
	return next, cmd
}

func (m Model) route(msg tea.Msg) (Model, tea.Cmd) {
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
		m.pulse++
		return m, cmd

	// One message type for every fact: the UI learns everything the same way.
	case factMsg:
		spinning := m.spinning()
		for _, e := range msg.events {
			m.apply(e)
		}
		// A listing may have put another session under the picker's cursor.
		load := tea.Batch(m.loadSelected(), m.reloadReview())
		var cmd tea.Cmd
		// Edge only: Tick carries the live tag, so re-arming restarts
		// the chain and costs a frame.
		if m.spinning() && !spinning {
			cmd = m.spinner.Tick
		}
		return m, tea.Batch(cmd, load, nextFact(m.facts))

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

type welcomeTickMsg struct{}

// welcomeTick advances the boot pane's animation, and only while that
// pane is what is on screen.
func welcomeTick() tea.Cmd {
	return tea.Tick(welcomeFrameEvery, func(time.Time) tea.Msg { return welcomeTickMsg{} })
}

// rows flattens every block's calls into the one list the cursor
// indexes into.
func (m Model) rows() []*historyRow {
	n := 0
	for _, b := range m.blocks {
		n += len(b.rows)
	}
	out := make([]*historyRow, 0, n)
	for _, b := range m.blocks {
		out = append(out, b.shown()...)
	}
	return out
}

func (m Model) focused() *historyRow {
	rows := m.rows()
	if m.nav.cursor < 0 || m.nav.cursor >= len(rows) {
		return nil
	}
	return rows[m.nav.cursor]
}

// spinning reports whether anything on screen is still turning: the
// model thinking, a tool call running, or a command the human ran.
func (m Model) spinning() bool {
	return m.waiting || slices.ContainsFunc(m.blocks, anyRunning)
}

// userCommandRunning is whether esc has a command of the human's to stop.
func (m Model) userCommandRunning() bool {
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

// backToInput returns to the prompt, or to an engine question that
// arrived while the human was busy with something of their own.
func (m *Model) backToInput() {
	m.mode = modeInput
	m.nav.focus = focusInput
	m.prompt.Focus()
	m.raiseWaiting()
}

// raise puts an engine question up, unless the human is in something they
// opened or has typed into the bar: a key meant for either must not answer it.
func (m *Model) raise(q mode) {
	if m.askingOwn() || m.typing() {
		return
	}
	m.mode, m.askedAt = q, time.Now()
}

// raiseWaiting puts up a question that was held, once nothing holds it.
func (m *Model) raiseWaiting() {
	if m.mode != modeInput {
		return
	}
	switch {
	case m.asking() != nil:
		m.raise(modeConfirm)
	case m.bound != nil:
		m.raise(modeBound)
	}
}

// asking is the approval the box shows, the first one queued.
func (m Model) asking() *event.ApprovalAsked {
	if len(m.asked) == 0 {
		return nil
	}
	return &m.asked[0]
}

// unask takes a call's question off the queue. When it was the one shown,
// the next starts fresh: unscrolled, unread, and settling again.
func (m *Model) unask(call uuid.UUID) {
	if i := slices.IndexFunc(m.childAsks, func(a event.ApprovalAsked) bool { return a.ToolCall == call }); i >= 0 {
		a := m.agents[m.childAsks[i].Agent]
		m.childAsks = slices.Delete(m.childAsks, i, i+1)
		if a != nil {
			if !m.blocked(a) {
				a.waitingSince = time.Time{}
			}
			m.touch(a)
		}
		return
	}
	i := slices.IndexFunc(m.asked, func(a event.ApprovalAsked) bool { return a.ToolCall == call })
	if i < 0 {
		return
	}
	m.asked = slices.Delete(m.asked, i, i+1)
	if i > 0 {
		return
	}
	m.confirm = confirmState{}
	if m.mode == modeConfirm {
		m.backToInput()
	}
}

// typing is whether the bar holds something the human has not sent yet.
func (m Model) typing() bool {
	return m.mode == modeInput && strings.TrimSpace(m.prompt.Value()) != ""
}

// settling is whether a question appeared too recently for a key to be an
// answer to it rather than the next key of something typed.
func (m Model) settling() bool { return time.Since(m.askedAt) < questionSettle }

// askingOwn is whether the human is in something they opened, which a fact
// must not pull out from under them: a key typed there must never answer it.
func (m Model) askingOwn() bool {
	return m.mode == modeUndo || m.mode == modeForget || m.inModal()
}

// trackNewest keeps up with a new row only while following, so a human reading
// back through history is never pulled away by what the model does next.
func (m *Model) trackNewest() {
	if !m.nav.follow || m.nav.focus == focusOutput {
		return
	}
	m.nav.cursor = len(m.rows()) - 1
}

// followNewest goes back to following, for what the human started: a request,
// a resumed session, or end.
func (m *Model) followNewest() {
	m.nav.follow = true
	m.trackNewest()
}
