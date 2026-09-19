package tui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/vitzeno/detent/internal/loop"
)

// screen is which of §8's states is currently shown. Idle/Typing are
// collapsed into one screenInput — a standalone TUI's goal field is
// always focused and ready, so there's no separate "closed overlay"
// state to model.
type screen int

const (
	screenInput screen = iota
	screenRunning
	screenConfirm
	screenAmbiguous
	screenFinished
)

// Model is the whole app. Jev output never reaches the screen directly
// (§8) — every field here is either a value the loop already computed or
// pure UI state (cursor position, spinner frame).
type Model struct {
	l   *loop.Loop
	ctx context.Context

	screen        screen
	width, height int

	input   textinput.Model
	spinner spinner.Model

	stepProgress  progress.Model
	writeProgress progress.Model

	run     *loop.Run
	waiting bool // a Cmd is in flight — controls the spinner and input lock

	// The step currently being prepared/committed/resolved, and what to
	// call it once done — set right before dispatching the matching Cmd,
	// read back when its Msg returns.
	active string // human label for the spinner line: "thinking…" or a capability name

	pending *loop.Prepared // awaiting confirm or ambiguous resolution

	ambiguousCursor int

	showFindings bool // [v] toggle, §4.3/§8.1

	termination *loop.Termination
	fatalErr    error
	aborted     bool
}

// New builds the initial Model. l must be fully wired (registries,
// providers, budgets, gate rules) — the same Loop the CLI path uses,
// just driven step by step instead of via l.Run.
func New(ctx context.Context, l *loop.Loop) Model {
	ti := textinput.New()
	// A read-only example on purpose — this is the first thing anyone
	// sees, before they've learned that destructive actions are gated by
	// a real confirm. A "kill" example here looked like an invitation.
	ti.Placeholder = "what files are in this directory?"
	ti.Focus()
	ti.CharLimit = 500
	ti.Prompt = "▏ "
	ti.PromptStyle = styleAccent
	ti.TextStyle = styleGoal

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styleAccent

	stepProg := progress.New(progress.WithSolidFill(string(accent)))
	stepProg.Width = 20
	writeProg := progress.New(progress.WithSolidFill(string(danger)))
	writeProg.Width = 20

	return Model{
		l:             l,
		ctx:           ctx,
		screen:        screenInput,
		input:         ti,
		spinner:       sp,
		stepProgress:  stepProg,
		writeProgress: writeProg,
	}
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = min(msg.Width-6, 70)
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

	case progress.FrameMsg:
		spModel, cmd1 := m.stepProgress.Update(msg)
		m.stepProgress = spModel.(progress.Model)
		wpModel, cmd2 := m.writeProgress.Update(msg)
		m.writeProgress = wpModel.(progress.Model)
		return m, tea.Batch(cmd1, cmd2)

	case preparedMsg:
		return m.onPrepared(msg)
	case committedMsg:
		return m.onCommitted(msg)
	case resolvedMsg:
		return m.onResolved(msg)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	}

	switch m.screen {
	case screenInput:
		return m.handleInputKey(msg)
	case screenRunning:
		return m.handleRunningKey(msg)
	case screenConfirm:
		return m.handleConfirmKey(msg)
	case screenAmbiguous:
		return m.handleAmbiguousKey(msg)
	case screenFinished:
		return m.handleFinishedKey(msg)
	}
	return m, nil
}

// resetForNewGoal returns to the input screen for another goal, clearing
// every field a finished run left behind — without this, a second run
// would carry over the previous one's termination, findings-toggle, and
// stale Run pointer. Only the Loop itself (m.l — registries, providers,
// budgets) survives between goals; everything per-run does not.
func (m Model) resetForNewGoal() Model {
	m.screen = screenInput
	m.run = nil
	m.pending = nil
	m.termination = nil
	m.fatalErr = nil
	m.aborted = false
	m.showFindings = false
	m.waiting = false
	m.ambiguousCursor = 0
	m.input.SetValue("")
	m.input.Focus()
	return m
}

func (m Model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		goal := m.input.Value()
		if goal == "" {
			return m, nil
		}
		m.run = m.l.NewRun(goal)
		m.screen = screenRunning
		m.waiting = true
		m.active = "thinking"
		return m, tea.Batch(m.spinner.Tick, prepareCmd(m.ctx, m.run))
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handleRunningKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.aborted = true
		m.screen = screenFinished
		return m, nil
	case "v":
		m.showFindings = !m.showFindings
	}
	return m, nil
}

func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		prepared := m.pending
		m.pending = nil
		m.screen = screenRunning
		m.waiting = true
		m.active = prepared.Capability
		return m, tea.Batch(m.spinner.Tick, commitCmd(m.ctx, m.run, prepared))
	case "n", "esc":
		term := m.run.Decline(m.pending.Capability)
		m.termination = &term
		m.pending = nil
		m.screen = screenFinished
		return m, nil
	case "v":
		m.showFindings = !m.showFindings
	}
	return m, nil
}

func (m Model) handleAmbiguousKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.pending.Ambiguous.Candidates)
	switch msg.String() {
	case "up", "k":
		if m.ambiguousCursor > 0 {
			m.ambiguousCursor--
		}
	case "down", "j":
		if m.ambiguousCursor < n-1 {
			m.ambiguousCursor++
		}
	case "enter":
		candidate := m.pending.Ambiguous.Candidates[m.ambiguousCursor]
		prepared := m.pending
		m.waiting = true
		m.active = "resolving"
		return m, tea.Batch(m.spinner.Tick, resolveCmd(m.run, prepared, candidate.ID))
	case "esc":
		m.aborted = true
		m.screen = screenFinished
		return m, nil
	}
	return m, nil
}

func (m Model) handleFinishedKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		return m.resetForNewGoal(), textinput.Blink
	case "q":
		return m, tea.Quit
	case "v":
		m.showFindings = !m.showFindings
	}
	return m, nil
}

func (m Model) onPrepared(msg preparedMsg) (tea.Model, tea.Cmd) {
	if m.screen == screenFinished {
		return m, nil // aborted or already finished — ignore a late arrival
	}
	m.waiting = false
	if msg.err != nil {
		m.fatalErr = msg.err
		m.screen = screenFinished
		return m, nil
	}
	if msg.termination != nil {
		m.termination = msg.termination
		m.screen = screenFinished
		return m, nil
	}

	prepared := msg.prepared
	if prepared.Ambiguous != nil {
		m.pending = prepared
		m.ambiguousCursor = 0
		m.screen = screenAmbiguous
		return m, nil
	}
	if prepared.Confirm != nil {
		m.pending = prepared
		m.screen = screenConfirm
		return m, nil
	}

	m.waiting = true
	m.active = prepared.Capability
	return m, tea.Batch(m.spinner.Tick, commitCmd(m.ctx, m.run, prepared))
}

func (m Model) onCommitted(msg committedMsg) (tea.Model, tea.Cmd) {
	if m.screen == screenFinished {
		return m, nil
	}
	m.waiting = false
	if msg.err != nil {
		m.fatalErr = msg.err
		m.screen = screenFinished
		return m, nil
	}

	m.waiting = true
	m.active = "thinking"
	return m, tea.Batch(m.spinner.Tick, prepareCmd(m.ctx, m.run))
}

func (m Model) onResolved(msg resolvedMsg) (tea.Model, tea.Cmd) {
	if m.screen == screenFinished {
		return m, nil
	}
	m.waiting = false
	if msg.err != nil {
		m.fatalErr = msg.err
		m.screen = screenFinished
		return m, nil
	}

	prepared := msg.prepared
	if prepared.Confirm != nil {
		m.pending = prepared
		m.screen = screenConfirm
		return m, nil
	}

	m.screen = screenRunning
	m.waiting = true
	m.active = prepared.Capability
	return m, tea.Batch(m.spinner.Tick, commitCmd(m.ctx, m.run, prepared))
}

// summarizeFacts turns one reducer's arbitrary Facts map into the
// one-line summary §8.1's walked example collapses a completed step to
// ("pid 4821 · node server.js"). Falls back to a generic count when the
// shape isn't one of the known reducers' — new reducers don't break this,
// they just render less specifically until taught a shape.
func summarizeFacts(facts map[string]any) string {
	if facts == nil {
		return "(no findings)"
	}
	if v, ok := facts["preview"].(string); ok && v != "" {
		return truncateLine(v, 60)
	}
	// reduce.ProcessLines/PathLines build these as []map[string]any and
	// []string respectively — not []any — so the type assertion has to
	// match the reducer's real return type, not a generic slice shape.
	if procs, ok := facts["processes"].([]map[string]any); ok && len(procs) > 0 {
		count := fmt.Sprintf("%v processes", facts["count"])
		if total, ok := facts["total_found"]; ok {
			count = fmt.Sprintf("%v of %v processes", facts["count"], total)
		}
		return fmt.Sprintf("%s, e.g. pid %v (%v)", count, procs[0]["pid"], procs[0]["cmd"])
	}
	if paths, ok := facts["paths"].([]string); ok && len(paths) > 0 {
		return fmt.Sprintf("%v items, e.g. %v", facts["count"], paths[0])
	}
	if out, ok := facts["output"].(string); ok {
		return truncateLine(out, 60)
	}
	if c, ok := facts["count"]; ok {
		return fmt.Sprintf("%v items", c)
	}
	return "done"
}

func truncateLine(s string, n int) string {
	for i, r := range s {
		if r == '\n' {
			s = s[:i]
			break
		}
	}
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
