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
	"github.com/vitzeno/detent/internal/ui/editor"
	"github.com/vitzeno/detent/internal/ui/slash"
	"github.com/vitzeno/detent/internal/ui/status"
	"github.com/vitzeno/detent/internal/ui/tree"
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
	// modeSaveConfirm shows a diff instead of a command, for a
	// direct file write the harness makes itself — never model-proposed
	// (see saveConfirmBox and Driver.RecordFileSave).
	modeSaveConfirm
)

type stepRow struct {
	command   string
	rationale string
	pre       agentloop.PreJudgment
	live      []string
	dropped   int
	// ec is the canonical record once Execute returns it — nil while
	// running. Result and Post read through it rather than duplicating
	// it, so a post-judgment write (see judgeMsg below) lands on the
	// same GoalResult.Commands entry agentloop and any other consumer
	// sees, instead of a UI-only copy.
	ec       *agentloop.ExecutedCommand
	running  bool
	expanded bool
	usage    *usage.Step
	// Component state for the detail zone.
	tableCursor int    // selected table row
	styled      string // cached transformed output (markdown/JSON/colors)
	styledWidth int    // viewport width the cache was built for

	// editPath is set at approve() time from the proposal's File field —
	// known before the command even runs, so the row's identity as
	// "this one edits a file" is stable throughout. editor is filled in
	// once the command finishes and the file is read from disk — the
	// same reusable component a tree-opened file uses.
	editPath string
	editor   *editor.Model

	// toolKind is non-empty for a slash-command's own row (a /tree,
	// /usage, or /help invocation, or a file opened by selecting it in
	// a tree) instead of a real executed command — set instead of ec,
	// never both. command holds whatever's worth showing in the history
	// line (a path, "/usage") since there's no real shell command text.
	toolKind string
	tree     *tree.Model // populated for toolKind == "tree"
	// usageCursor/usageExpand are toolKind == "usage"'s own navigation
	// state — which goal row is selected and which one (if any) is
	// expanded — kept per-row so more than one /usage invocation could
	// coexist with independent state, same as a table row's own cursor.
	usageCursor int
	usageExpand int
}

type goalBlock struct {
	goal      string
	res       *agentloop.GoalResult // nil for a tool block
	steps     []*stepRow
	ended     bool
	end       agentloop.EndReason
	summary   string
	judgeNote string
	fatalErr  error

	// tool names a slash-command invocation (/tree, /usage, /help, or a
	// file opened from a tree) rather than a goal driven by propose/
	// confirm/execute — "" for a real goal. Created already ended, never
	// becomes m.cur, and skips the goal-header/banner lines in history:
	// its one step's own line is the whole entry.
	tool string
}

type beginGoalMsg struct {
	goal string
	res  *agentloop.GoalResult
	err  error
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
	ec  *agentloop.ExecutedCommand
	err error
}

type judgeMsg struct {
	row  *stepRow
	post agentloop.PostJudgment
}

type saveDoneMsg struct {
	row     *stepRow
	content string
	err     error
}

// Driver is the session surface ui needs.
type Driver interface {
	BeginGoal(ctx context.Context, goal string) (*agentloop.GoalResult, error)
	ProposeNext(ctx context.Context, goal string) (propose.Proposal, agentloop.PreJudgment, usage.Usage, error)
	Execute(ctx context.Context, res *agentloop.GoalResult, ustep *usage.Step, p propose.Proposal, pre agentloop.PreJudgment, onEvent func(shell.StreamEvent)) (*agentloop.ExecutedCommand, error)
	JudgeResult(ctx context.Context, goal, command string, result shell.Result) agentloop.PostJudgment
	RecordDecline(res *agentloop.GoalResult, command string)
	RecordDone(res *agentloop.GoalResult, p propose.Proposal)
	RecordProposerError(res *agentloop.GoalResult, err error)
	// RecordFileSave notes a direct editor save in the transcript —
	// never a proposed command, see internal/editfile.Write.
	RecordFileSave(path, diff string)
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

	// editing is true once the focused row's editor has been explicitly
	// entered (not just loaded) — while true it owns every key ahead of
	// mode/focus, the same way modeConfirm does. saveRow is which row's
	// editor modeSaveConfirm is showing a diff for.
	editing bool
	saveRow *stepRow

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

	// Body row outer widths (history and output sit side by side) —
	// set once per resize by sizeViewport, read by baseView's island.Render
	// calls and by history.go/view.go's width-dependent truncation.
	// Never assume a pane's width equals m.width; only the full-width
	// overlays (confirm box, usage overlay) may still use m.width itself.
	outputColW int
	histColW   int

	// focus decides who owns single-letter keys. Typing and shortcuts
	// can't share one focus, so tab toggles: input owns text, history
	// owns arrows/space/v/q. Slash commands work from input regardless.
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
	if r == nil || r.running || r.ec == nil || r.ec.Post == nil {
		return ""
	}
	return r.ec.Post.RenderKind
}

// tableText returns the text a table parses from, and whether this row
// is table-kind. Key handling uses this instead of reaching into the
// classification vocabulary itself.
func (r *stepRow) tableText() (string, bool) {
	if rowKind(r) != agentloop.KindTable {
		return "", false
	}
	if r.ec.Result.Stdout != "" {
		return r.ec.Result.Stdout, true
	}
	return r.ec.Result.Stderr, true
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

	case beginGoalMsg:
		return m.onBeginGoal(msg)

	case proposeMsg:
		return m.onPropose(msg)

	case streamMsg:
		return m.onStream(msg)

	case execDoneMsg:
		return m.onExecDone(msg)

	case judgeMsg:
		// Written onto the shared ExecutedCommand, not a UI-only copy —
		// GoalResult.Commands carries the judgment too, the same as the
		// headless path.
		msg.row.ec.Post = &msg.post
		msg.row.usage.SetJudgePost(msg.post.JudgeUsage, msg.post.Attention, msg.post.GoalAchieved)
		if msg.post.Attention >= status.AttentionThreshold {
			msg.row.expanded = true
		}
		m.refreshViewport()
		return m, nil

	case saveDoneMsg:
		return m.onSaveDone(msg)
	}

	var cmd tea.Cmd
	m.output, cmd = m.output.Update(msg)
	return m, cmd
}
