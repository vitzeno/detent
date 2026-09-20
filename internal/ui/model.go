package ui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vitzeno/detent/internal/agent"
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

// cmdState is a stepRow's fields for an actually-executed command —
// zero-valued and unused on a tool row (toolKind != "").
type cmdState struct {
	rationale string
	pre       agent.PreJudgment
	live      []string
	dropped   int
	// ec is the canonical record once Execute returns it — nil while
	// running. Result and Post read through it rather than duplicating
	// it, so a post-judgment write (see judgeMsg below) lands on the
	// same GoalResult.Commands entry agent and any other consumer
	// sees, instead of a UI-only copy.
	ec       *agent.ExecutedCommand
	running  bool
	expanded bool
	usage    *usage.Step
	// Component state for the detail zone.
	tableCursor int    // selected table row
	styled      string // cached transformed output (markdown/JSON/colors)
	styledWidth int    // viewport width the cache was built for
}

// toolState is a stepRow's fields for a slash-command's own row (a
// /tree, /usage, or /help invocation, or a file opened from a tree) —
// zero-valued and unused on a command row (toolKind == "").
type toolState struct {
	tree *tree.Model // populated for toolKind == "tree"
	// usageCursor/usageExpand are toolKind == "usage"'s own navigation
	// state — which goal row is selected and which one (if any) is
	// expanded — kept per-row so more than one /usage invocation could
	// coexist with independent state, same as a table row's own cursor.
	usageCursor int
	usageExpand int
}

type stepRow struct {
	command string

	// editPath is set at approve() time from the proposal's File field —
	// known before the command even runs, so the row's identity as
	// "this one edits a file" is stable throughout. editor is filled in
	// once the command finishes and the file is read from disk — the
	// same reusable component a tree-opened file uses. Shared by both
	// a command row and a tool row (a file opened from a tree uses it
	// too), unlike cmd/tool below.
	editPath string
	editor   *editor.Model

	// toolKind is non-empty for a tool row instead of a real executed
	// command — command holds whatever's worth showing in the history
	// line (a path, "/usage") since there's no real shell command text.
	// Exactly one of cmd/tool is meaningful, discriminated by this field;
	// the other stays at its zero value.
	toolKind string
	cmd      cmdState
	tool     toolState
}

type goalBlock struct {
	goal      string
	res       *agent.GoalResult // nil for a tool block
	steps     []*stepRow
	ended     bool
	end       agent.EndReason
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
	res  *agent.GoalResult
	err  error
}

type proposeMsg struct {
	proposal propose.Proposal
	pre      agent.PreJudgment
	used     usage.Usage
	err      error
}

type streamMsg struct {
	stderr bool
	line   string
}

type execDoneMsg struct {
	ec  *agent.ExecutedCommand
	err error
}

type judgeMsg struct {
	row  *stepRow
	post agent.PostJudgment
}

type saveDoneMsg struct {
	row     *stepRow
	content string
	err     error
}

// Driver is the session surface ui needs.
type Driver interface {
	BeginGoal(ctx context.Context, goal string) (*agent.GoalResult, error)
	ProposeNext(ctx context.Context, goal string) (propose.Proposal, agent.PreJudgment, usage.Usage, error)
	Execute(ctx context.Context, res *agent.GoalResult, ustep *usage.Step, p propose.Proposal, pre agent.PreJudgment, onEvent func(shell.StreamEvent)) (*agent.ExecutedCommand, error)
	JudgeResult(ctx context.Context, goal, command string, result shell.Result) agent.PostJudgment
	RecordDecline(res *agent.GoalResult, command string)
	RecordDone(res *agent.GoalResult, p propose.Proposal)
	RecordProposerError(res *agent.GoalResult, err error)
	// RecordFileSave notes a direct editor save in the transcript —
	// never a proposed command, see internal/fileio.Write.
	RecordFileSave(path, diff string)
	// RecordAbort closes out a goal the human aborted (Esc or /abort);
	// res is nil when the abort landed before BeginGoal produced one.
	RecordAbort(res *agent.GoalResult)
	Tracker() *usage.Tracker
}

// navState is history/output navigation: which row is focused, whether
// the view auto-follows new content, and the cached history-pane
// render. History windowing keeps the cursor visible — histWindow/
// histOffset/cursorLine are all computed and cached by
// updateHistoryWindow (a *Model method reached via refreshViewport),
// never by View()'s own call chain, which runs on a throwaway copy and
// can't persist them.
type navState struct {
	cursor int
	follow bool
	// focus decides who owns single-letter keys. Typing and shortcuts
	// can't share one focus, so tab toggles: input owns text, history
	// owns arrows/space/v/q. Slash commands work from input regardless.
	focus focusPane

	histHeight int
	histOffset int
	cursorLine int
	histWindow []string
}

// layoutState is the body row's pane widths, recomputed by sizeViewport
// on every resize — history and output sit side by side. Never assume
// a pane's width equals width; only the full-width overlays (confirm
// box, usage overlay) may still use width itself.
type layoutState struct {
	width, height int
	outputColW    int
	histColW      int
}

// confirmState is the propose→confirm handoff: what's pending
// approval, and how long the human took to decide (dwell).
type confirmState struct {
	pending propose.Proposal
	pre     agent.PreJudgment
	use     usage.Usage
	// shownAt starts the human-dwell clock on the modal.
	shownAt time.Time
}

// saveState is the direct-editor-write confirm flow. editing is true
// once the focused row's editor has been explicitly entered (not just
// loaded) — while true it owns every key ahead of mode/focus, the same
// way modeConfirm does. row is which row's editor modeSaveConfirm is
// showing a diff for.
type saveState struct {
	editing bool
	row     *stepRow
}

// slashState is the input bar's "/"-command autocomplete: current
// prefix matches plus the highlighted entry. Open whenever non-empty
// while typing a "/" command.
type slashState struct {
	matches []slash.Cmd
	cursor  int
}

// perfState is UI-prep cost, measured around viewport refreshes.
type perfState struct {
	uiPrep  time.Duration
	uiPreps int
}

// Model is the TUI state.
type Model struct {
	sess Driver
	ctx  context.Context

	proposerName string
	judgeName    string

	input   textinput.Model
	output  viewport.Model
	spinner spinner.Model

	blocks []*goalBlock
	cur    *goalBlock

	// mode is shared across the confirm and save-confirm flows (and
	// plain input) rather than living inside either's own state — it's
	// the one switch that decides which of them, if any, owns the
	// screen right now.
	mode    mode
	waiting bool
	abort   context.CancelFunc

	nav     navState
	layout  layoutState
	confirm confirmState
	save    saveState
	slash   slashState
	perf    perfState

	streamCh chan streamMsg

	notice string // one-shot status flash, e.g. unknown slash command

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
	if r == nil || r.cmd.running || r.cmd.ec == nil || r.cmd.ec.Post == nil {
		return ""
	}
	return r.cmd.ec.Post.RenderKind
}

// tableText returns the text a table parses from, and whether this row
// is table-kind. Key handling uses this instead of reaching into the
// classification vocabulary itself.
func (r *stepRow) tableText() (string, bool) {
	if rowKind(r) != agent.KindTable {
		return "", false
	}
	if r.cmd.ec.Result.Stdout != "" {
		return r.cmd.ec.Result.Stdout, true
	}
	return r.cmd.ec.Result.Stderr, true
}

// New builds the TUI over an agent session.
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
		nav:          navState{follow: true},
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
	if m.nav.cursor < 0 {
		m.nav.cursor = 0
	}
	if m.nav.cursor >= len(rows) {
		m.nav.cursor = len(rows) - 1
	}
	return rows[m.nav.cursor]
}

func (m *Model) trackNewest() {
	// Whatever else this call does, new content (a just-submitted goal,
	// a just-appended step) must reach the cached history window before
	// this returns — refreshViewport is the only thing that recomputes
	// it, and unlike most other mutation sites, callers of trackNewest
	// (startGoal in particular) don't otherwise call it themselves.
	defer m.refreshViewport()
	if !m.nav.follow {
		return
	}
	// A reader parked in the output pane stays parked: jumping the
	// cursor on every new command yanks the row out from under them.
	// Unfollowing too, so the history stops scrolling past as well —
	// navigating back to the bottom re-follows naturally.
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
		// The history pane's spinner (the "thinking…" line, or a running
		// row's own icon) is baked into nav.histWindow, a cache that only
		// refreshViewport recomputes — a spinner tick otherwise changes
		// nothing refreshViewport is normally invoked for, so without this
		// the cached frame freezes at whatever it was on the last real
		// content change while the status bar's own spinner (computed
		// fresh every View()) keeps animating right next to it.
		m.refreshViewport()
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
		msg.row.cmd.ec.Post = &msg.post
		msg.row.cmd.usage.SetJudgePost(msg.post.JudgeUsage, msg.post.Attention, msg.post.GoalAchieved)
		if msg.post.Attention >= status.AttentionThreshold {
			msg.row.cmd.expanded = true
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
