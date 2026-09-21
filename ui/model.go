// Package ui is a full-screen dynamic TUI: an output pane and a
// history pane over an input bar, driven by one Model. It imports
// nothing under internal/ — internal/resolver translates between this
// package's DTOs (driver.go) and the harness's own types.
package ui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/status"
	"github.com/vitzeno/detent/ui/welcome"
)

// Model is the TUI state. The screen is three zones — an output pane,
// a history pane, and the input bar — over one flat list of goal
// blocks; blocks.go holds those, messages.go what arrives about them.
type Model struct {
	sess Driver
	ctx  context.Context

	info SessionInfo

	prompt  prompt
	output  viewport.Model
	spinner spinner.Model

	blocks []*goalBlock
	cur    *goalBlock

	mode    mode
	waiting bool
	abort   context.CancelFunc

	nav      navState
	layout   layoutState
	confirm  confirmState
	save     saveState
	rollback rollbackState
	perf     perfState

	streamCh chan StreamEvent

	// welcomeFrame advances the boot pane's detent animation; it only
	// ticks while that pane is the thing on screen.
	welcomeFrame int

	notice noticeState // one-shot status flash

	viewContent string // last rendered viewport content, avoids scroll resets

	totalCmds int
}

// SessionInfo is what the session bar reports about this run. Grouped
// rather than passed as three bare strings, which read identically at
// a call site and so swap silently.
type SessionInfo struct {
	Proposer string
	Judge    string // "" when no judge is wired
	RunMode  string // "host" or "sandbox"

	// Sandbox facts for the welcome pane; empty in host mode.
	Image   string
	Mount   string
	Runtime string // "" means containerd's own default
	Network string // sandbox.Network* — "host" shares the daemon's network
}

// New builds the TUI over sess.
func New(ctx context.Context, sess Driver, info SessionInfo) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(accent)

	return Model{
		sess:     sess,
		ctx:      ctx,
		info:     info,
		prompt:   newPrompt(),
		output:   viewport.New(),
		spinner:  sp,
		streamCh: make(chan StreamEvent, streamBufSize),
		nav:      navState{follow: true},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, welcomeTick())
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

// route hands each message to the flow that owns it: keys.go for
// keystrokes, goal_flow.go for the propose→confirm→execute→judge
// sequence, exec_flow/save_flow/rollback_flow for the rest.
func (m Model) route(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.layout.width, m.layout.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.KeyboardEnhancementsMsg:
		// The terminal answered our request; only now do we know
		// whether shift+enter is a key of its own here.
		m.prompt.SetRichKeys(msg.SupportsKeyDisambiguation())
		return m, nil

	case spinner.TickMsg:
		if !m.waiting {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case beginGoalMsg:
		return m.onBeginGoal(msg)

	case proposeMsg:
		return m.onPropose(msg)

	case StreamEvent:
		return m.onStream(msg)

	case execDoneMsg:
		return m.onExecDone(msg)

	case judgeMsg:
		// Same object GoalResult.Commands holds, not a UI-only copy.
		msg.row.cmd.ec.Post = &msg.post
		if msg.post.Attention >= status.AttentionThreshold {
			msg.row.cmd.expanded = true
		}
		// render_kind is known now, which is what prunes the vocabulary
		// a generated view may draw from.
		return m, m.generateView(msg.row)

	case viewMsg:
		applyView(msg.row, msg.spec)
		return m, nil

	case saveDoneMsg:
		return m.onSaveDone(msg)

	case rollbackDoneMsg:
		return m.onRollbackDone(msg)

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

// rows flattens every block's steps into the one list the cursor
// indexes into.
func (m *Model) rows() []*stepRow {
	var out []*stepRow
	for _, b := range m.blocks {
		out = append(out, b.steps...)
	}
	return out
}

// focused is the row the cursor is on, clamping the cursor if rows
// have come or gone since it was last set.
func (m *Model) focused() *stepRow {
	rows := m.rows()
	if len(rows) == 0 {
		return nil
	}
	if m.nav.cursor < 0 {
		m.nav.cursor = 0
	}
	if m.nav.cursor >= len(rows) {
		m.nav.cursor = len(rows) - 1
	}
	return rows[m.nav.cursor]
}

// trackNewest follows the latest row, unless the human has taken over.
func (m *Model) trackNewest() {
	if !m.nav.follow {
		return
	}
	// A reader parked in the output pane stays parked.
	if m.nav.focus == focusOutput {
		m.nav.follow = false
		return
	}
	m.nav.cursor = len(m.rows()) - 1
}

// welcomeTick re-arms itself only while the welcome pane is showing,
// so an idle animation never outlives the screen it belongs to.
func welcomeTick() tea.Cmd {
	return tea.Tick(welcome.TickRate, func(time.Time) tea.Msg { return welcomeTickMsg{} })
}

const (
	maxLiveLines  = 1000
	streamBufSize = 2048
)

// mode is which of the three input states the bottom zone is in.
type mode int

const (
	modeInput mode = iota
	modeConfirm
	modeSaveConfirm     // diff confirm for a direct editor save, see saveConfirmBox
	modeRollbackConfirm // asks before reverting the human's own files
)

// focusPane is which zone the arrow keys act in.
type focusPane int

const (
	focusInput focusPane = iota
	focusHistory
	focusOutput
)

// navState is history/output navigation. histOffset is the only
// scroll state kept: what's visible is derived per render by
// historyWindow, so there is no cache to keep in step.
type navState struct {
	cursor int
	follow bool
	focus  focusPane

	histHeight int
	histOffset int
}

// layoutState is the body row's pane widths, recomputed by sizeViewport.
type layoutState struct {
	width, height int
	outputColW    int
	histColW      int
}

// confirmState is the propose→confirm handoff.
type confirmState struct {
	pending Proposal
	pre     PreJudgment
	use     Usage
	shownAt time.Time // starts the dwell clock
}

// saveState is the direct-editor-write confirm flow.
type saveState struct {
	editing bool
	row     *stepRow
}

// rollbackState is a rollback waiting on the human to say whether
// their own files go back with the container.
type rollbackState struct {
	target *goalBlock
	local  int
	step   int
	files  []FileChange
}

// perfState is UI-prep cost, measured around viewport refreshes.
type perfState struct {
	uiPrep  time.Duration
	uiPreps int
}

// noticeState is the one-shot status flash: text and outcome in one
// value so they can't disagree. Set it via noteOK/noteErr.
type noticeState struct {
	text string
	bad  bool
}

func (m *Model) noteOK(text string)  { m.notice = noticeState{text: text} }
func (m *Model) noteErr(text string) { m.notice = noticeState{text: text, bad: true} }
func (m *Model) clearNotice()        { m.notice = noticeState{} }
