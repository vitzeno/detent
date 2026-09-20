package ui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vitzeno/detent/internal/ui/editor"
	"github.com/vitzeno/detent/internal/ui/slash"
	"github.com/vitzeno/detent/internal/ui/status"
	"github.com/vitzeno/detent/internal/ui/tree"
)

const (
	maxLiveLines     = 1000
	maxViewportLines = 400
	streamBufSize    = 2048
)

type mode int

const (
	modeInput mode = iota
	modeConfirm
	modeSaveConfirm // diff confirm for a direct editor save, see saveConfirmBox
)

// cmdState holds a stepRow's fields for an executed command; zero-valued
// on a tool row (toolKind != "").
type cmdState struct {
	rationale string
	pre       PreJudgment
	live      []string
	dropped   int
	ec        *ExecutedCommand // nil while running; judgeMsg attaches Post
	running   bool
	expanded  bool
	step      StepHandle // correlates with Driver.RecordStep/JudgeResult

	tableCursor int    // selected table row
	styled      string // cached transformed output
	styledWidth int    // viewport width the cache was built for
}

// toolState holds a stepRow's fields for a slash-command row (/tree,
// /usage, /help, or a tree-opened file); zero-valued on a command row.
type toolState struct {
	tree        *tree.Model // toolKind == "tree"
	usageCursor int         // toolKind == "usage"
	usageExpand int
}

type stepRow struct {
	command string

	// editPath is set at approve() time; editor fills in once the
	// command finishes and the file is read from disk. Shared by both a
	// command row and a tree-opened file row.
	editPath string
	editor   *editor.Model

	// toolKind is non-empty for a tool row instead of an executed
	// command. Exactly one of cmd/tool is meaningful.
	toolKind string
	cmd      cmdState
	tool     toolState
}

type goalBlock struct {
	goal      string
	res       *GoalResult // nil for a tool block
	steps     []*stepRow
	ended     bool
	end       EndReason
	summary   string
	judgeNote string
	fatalErr  error

	// tool names a slash-command invocation. Empty for a real goal.
	tool string
}

type beginGoalMsg struct {
	goal string
	res  *GoalResult
	err  error
}

type proposeMsg struct {
	proposal Proposal
	pre      PreJudgment
	used     Usage
	err      error
}

type execDoneMsg struct {
	ec  *ExecutedCommand
	err error
}

type judgeMsg struct {
	row  *stepRow
	post PostJudgment
}

type saveDoneMsg struct {
	row     *stepRow
	content string
	err     error
}

type welcomeTickMsg struct{}

type rollbackDoneMsg struct {
	target *goalBlock
	step   int
	ok     bool
	err    error
}

// navState is history/output navigation. histWindow/histOffset/cursorLine
// get computed and cached by updateHistoryWindow via refreshViewport.
// View() itself runs on a throwaway copy of Model so it can't do this.
type navState struct {
	cursor int
	follow bool
	focus  focusPane

	histHeight int
	histOffset int
	cursorLine int
	histWindow []string
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

// slashState is the input bar's "/"-command autocomplete.
type slashState struct {
	matches []slash.Cmd
	cursor  int
}

// perfState is UI-prep cost, measured around viewport refreshes.
type perfState struct {
	uiPrep  time.Duration
	uiPreps int
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
}

// Model is the TUI state.
type Model struct {
	sess Driver
	ctx  context.Context

	info SessionInfo

	input   textarea.Model
	output  viewport.Model
	spinner spinner.Model

	blocks []*goalBlock
	cur    *goalBlock

	mode    mode
	waiting bool
	abort   context.CancelFunc

	nav     navState
	layout  layoutState
	confirm confirmState
	save    saveState
	slash   slashState
	perf    perfState

	streamCh chan StreamEvent

	// welcomeFrame advances the boot pane's detent animation; it only
	// ticks while that pane is the thing on screen.
	welcomeFrame int

	notice string // one-shot status flash

	viewContent string // last rendered viewport content, avoids scroll resets

	totalCmds int
}

type focusPane int

const (
	focusInput focusPane = iota
	focusHistory
	focusOutput
)

// rowKind returns the judged kind, or "" while pending.
func rowKind(r *stepRow) RenderKind {
	if r == nil || r.cmd.running || r.cmd.ec == nil || r.cmd.ec.Post == nil {
		return ""
	}
	return r.cmd.ec.Post.RenderKind
}

func (r *stepRow) tableText() (string, bool) {
	if rowKind(r) != KindTable {
		return "", false
	}
	if r.cmd.ec.Result.Stdout != "" {
		return r.cmd.ec.Result.Stdout, true
	}
	return r.cmd.ec.Result.Stderr, true
}

// New builds the TUI over sess.
func New(ctx context.Context, sess Driver, info SessionInfo) Model {
	ti := newInput()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(accent)

	return Model{
		sess:     sess,
		ctx:      ctx,
		info:     info,
		input:    ti,
		output:   viewport.New(0, 0),
		spinner:  sp,
		streamCh: make(chan StreamEvent, streamBufSize),
		nav:      navState{follow: true},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, welcomeTick())
}

// welcomeTick re-arms itself only while the welcome pane is showing,
// so an idle animation never outlives the screen it belongs to.
func welcomeTick() tea.Cmd {
	return tea.Tick(welcomeTickRate, func(time.Time) tea.Msg { return welcomeTickMsg{} })
}

func (m *Model) rows() []*stepRow {
	var out []*stepRow
	for _, b := range m.blocks {
		out = append(out, b.steps...)
	}
	return out
}

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

func (m *Model) trackNewest() {
	// Callers (startGoal in particular) rely on this to refresh the
	// cached history window themselves.
	defer m.refreshViewport()
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

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.layout.width, m.layout.height = msg.Width, msg.Height
		m.sizeViewport() // refits the input too, via syncInputSize
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		if !m.waiting {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		// History's spinner line is baked into the nav.histWindow cache;
		// without this it freezes while the status bar's own spinner
		// keeps animating.
		m.refreshViewport()
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
		m.refreshViewport()
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
