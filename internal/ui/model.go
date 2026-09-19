package ui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vitzeno/detent/internal/agentloop"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/shell"
	"github.com/vitzeno/detent/internal/ui/slash"
	"github.com/vitzeno/detent/internal/ui/status"
	"github.com/vitzeno/detent/internal/usage"
)

const (
	maxLiveLines     = 1000
	maxViewportLines = 400
	streamBufSize    = 2048
)

// mode is input vs confirm; propose/exec state stays orthogonal
// so navigation stays live while waiting.
type mode int

const (
	modeInput mode = iota
	modeConfirm
)

type stepRow struct {
	command   string
	rationale string
	pre       agentloop.PreJudgment
	live      []string
	dropped   int
	result    *shell.Result
	post      *agentloop.PostJudgment
	running   bool
	expanded  bool
	usage     *usage.Step
	// Component state for the detail zone.
	tableCursor int    // selected table row
	styled      string // cached transformed output (markdown/JSON/colors)
	styledWidth int    // viewport width the cache was built for
}

type goalBlock struct {
	goal      string
	res       *agentloop.GoalResult
	steps     []*stepRow
	ended     bool
	end       agentloop.EndReason
	summary   string
	judgeNote string
	fatalErr  error
}

type proposeMsg struct {
	proposal propose.Proposal
	pre      agentloop.PreJudgment
	used     usage.Usage
	err      error
}

type streamMsg struct {
	stderr bool
	line   string
}

type execDoneMsg struct {
	result shell.Result
	err    error
}

type judgeMsg struct {
	row  *stepRow
	post agentloop.PostJudgment
}

// Driver is the session surface ui needs.
type Driver interface {
	BeginGoal(goal string) (*agentloop.GoalResult, error)
	ProposeNext(ctx context.Context, goal string) (propose.Proposal, agentloop.PreJudgment, usage.Usage, error)
	Execute(ctx context.Context, res *agentloop.GoalResult, ustep *usage.Step, p propose.Proposal, pre agentloop.PreJudgment, onEvent func(shell.StreamEvent)) (*agentloop.ExecutedCommand, error)
	JudgeResult(ctx context.Context, goal, command string, result shell.Result) agentloop.PostJudgment
	RecordDecline(res *agentloop.GoalResult, command string)
	RecordDone(res *agentloop.GoalResult, p propose.Proposal)
	RecordProposerError(res *agentloop.GoalResult, err error)
	Tracker() *usage.Tracker
}

// Model is the TUI state.
type Model struct {
	sess Driver
	ctx  context.Context

	proposerName string
	judgeName    string

	width, height int
	input         textinput.Model
	output        viewport.Model
	spinner       spinner.Model

	blocks []*goalBlock
	cur    *goalBlock

	mode    mode
	waiting bool
	abort   context.CancelFunc

	pending    propose.Proposal
	pendingPre agentloop.PreJudgment
	pendingUse usage.Usage
	// confirmShownAt starts the human-dwell clock on the modal.
	confirmShownAt time.Time

	// Usage overlay state.
	showUsage   bool
	usageCursor int
	usageExpand int // expanded goal index, -1 when none

	// UI prep cost, measured around viewport refreshes.
	uiPrep  time.Duration
	uiPreps int

	streamCh chan streamMsg

	cursor int
	follow bool

	// History windowing keeps the cursor visible.
	histHeight int
	histOffset int
	cursorLine int

	// focus decides who owns single-letter keys. Typing and shortcuts
	// can't share one focus, so tab toggles: input owns text, history
	// owns j/k/space/v/q. Slash commands work from input regardless.
	focus  focusPane
	notice string // one-shot status flash, e.g. unknown slash command

	// Slash autocomplete: current prefix matches + highlight. Open
	// whenever non-empty while typing a "/" command.
	slash       []slash.Cmd
	slashCursor int

	// Last viewport content, to avoid scroll-resetting SetContent calls.
	viewContent string

	totalCmds int
}

type focusPane int

const (
	focusInput focusPane = iota
	focusHistory
	focusOutput
)

// rowKind returns the judged kind, or "" while pending. Streaming rows
// always show the live viewport regardless of kind.
func rowKind(r *stepRow) string {
	if r == nil || r.running || r.post == nil {
		return ""
	}
	return r.post.RenderKind
}

// tableText returns the text a table parses from, and whether this row
// is table-kind. Key handling uses this instead of reaching into the
// classification vocabulary itself.
func (r *stepRow) tableText() (string, bool) {
	if rowKind(r) != agentloop.KindTable || r.result == nil {
		return "", false
	}
	if r.result.Stdout != "" {
		return r.result.Stdout, true
	}
	return r.result.Stderr, true
}

// New builds the TUI over an agentloop session.
func New(ctx context.Context, sess Driver, proposerName, judgeName string) Model {
	ti := textinput.New()
	ti.Placeholder = "describe a goal, e.g. what is listening on port 3000?"
	ti.Focus()
	ti.CharLimit = 500
	ti.Prompt = "❯ "

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(accent)

	return Model{
		sess:         sess,
		ctx:          ctx,
		proposerName: proposerName,
		judgeName:    judgeName,
		input:        ti,
		output:       viewport.New(0, 0),
		spinner:      sp,
		streamCh:     make(chan streamMsg, streamBufSize),
		follow:       true,
		usageExpand:  -1,
	}
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
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
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(rows) {
		m.cursor = len(rows) - 1
	}
	return rows[m.cursor]
}

func (m *Model) trackNewest() {
	if !m.follow {
		return
	}
	// A reader parked in the output pane stays parked: jumping the
	// cursor on every new command yanks the row out from under them.
	// Unfollowing too, so the history stops scrolling past as well —
	// navigating back to the bottom re-follows naturally.
	if m.focus == focusOutput {
		m.follow = false
		return
	}
	m.cursor = len(m.rows()) - 1
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = msg.Width - 6
		m.sizeViewport()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		if !m.waiting {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case proposeMsg:
		return m.onPropose(msg)

	case streamMsg:
		return m.onStream(msg)

	case execDoneMsg:
		return m.onExecDone(msg)

	case judgeMsg:
		msg.row.post = &msg.post
		msg.row.usage.SetJudgePost(msg.post.JudgeUsage, msg.post.Attention, msg.post.GoalAchieved)
		if msg.post.Attention >= status.AttentionThreshold {
			msg.row.expanded = true
		}
		m.refreshViewport()
		return m, nil
	}

	var cmd tea.Cmd
	m.output, cmd = m.output.Update(msg)
	return m, cmd
}
