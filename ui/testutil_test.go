package ui

import (
	"context"
	"os"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Shared fixtures for every *_test.go file in this package.
//
// fakeDriver is a hand-written ui.Driver. ui's tests exercise the UI's
// state machine against it directly, never a real *agent.Session —
// resolver imports this package for its DTOs, so ui's own tests
// importing resolver back would be a cycle. Bonus: no agent/propose/
// host/usage/classify import needed here at all. resolver's own tests
// cover the real translation from agent's domain into these DTOs.
//
// Configure a fakeDriver per test by setting its fields after
// newFakeDriver() and before use.
type fakeDriver struct {
	// proposals is a script; ProposeNext walks it and repeats the last
	// entry once exhausted.
	proposals []Proposal
	proposeN  int
	// pre is returned for every non-Done proposal.
	pre PreJudgment

	beginErr   error
	proposeErr error

	execResult Result
	execErr    error
	post       PostJudgment

	rollbackOK  bool
	rollbackErr error
	rolledBackN int // records the step Rollback was last called with

	tracker []GoalStats
	snap    Snapshot

	// readOverride, when true, makes ReadFile return the fields below
	// verbatim instead of reading path from disk. Default reads real
	// content, since most tests want the real bytes.
	readOverride  bool
	readContent   string
	readTruncated bool
	readMaxBytes  int
	readErr       error

	// Recorded calls, for tests that assert on what was sent rather than
	// what came back.
	savedPath, savedDiff, savedContent string
	saveErr                            error
}

func newFakeDriver() *fakeDriver {
	return &fakeDriver{
		proposals: []Proposal{
			{Command: "ls -la", Rationale: "list files"},
			{Done: true, Summary: "saw two files"},
		},
		pre:        PreJudgment{Mutability: "read_only"},
		execResult: Result{Stdout: "a\nb\n"},
		post:       PostJudgment{FromJudge: true, Status: "clean_success", RenderKind: KindInline, Attention: 0.2, GoalAchieved: 0.9},
	}
}

func (f *fakeDriver) BeginGoal(_ context.Context, goal string) (*GoalResult, error) {
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	return &GoalResult{Goal: goal}, nil
}

func (f *fakeDriver) ProposeNext(_ context.Context, _ string) (Proposal, PreJudgment, Usage, error) {
	if f.proposeErr != nil {
		return Proposal{}, PreJudgment{}, Usage{}, f.proposeErr
	}
	p := f.proposals[f.proposeN]
	if f.proposeN < len(f.proposals)-1 {
		f.proposeN++
	}
	if p.Done {
		return p, PreJudgment{}, Usage{}, nil
	}
	return p, f.pre, Usage{}, nil
}

func (f *fakeDriver) RecordStep(_ *GoalResult, _ Proposal, _ PreJudgment, _ Usage, _ time.Duration) StepHandle {
	return StepHandle{}
}

func (f *fakeDriver) Execute(_ context.Context, res *GoalResult, _ StepHandle, p Proposal, pre PreJudgment, _ chan<- StreamEvent) (*ExecutedCommand, error) {
	if f.execErr != nil {
		return nil, f.execErr
	}
	ec := &ExecutedCommand{Command: p.Command, Result: f.execResult, Pre: pre}
	res.Commands = append(res.Commands, ec)
	return ec, nil
}

func (f *fakeDriver) JudgeResult(_ context.Context, _, _ string, _ Result, _ StepHandle) PostJudgment {
	return f.post
}

func (f *fakeDriver) RecordDecline(res *GoalResult, _ string) { res.End = EndDeclined }

func (f *fakeDriver) RecordDone(res *GoalResult, p Proposal) {
	res.Summary = p.Summary
	res.End = EndDone
}

func (f *fakeDriver) RecordProposerError(res *GoalResult, _ error) { res.End = EndProposerError }

func (f *fakeDriver) ReadFile(path string) (string, bool, int, error) {
	if f.readOverride {
		return f.readContent, f.readTruncated, f.readMaxBytes, f.readErr
	}
	b, err := os.ReadFile(path)
	return string(b), false, 0, err
}

func (f *fakeDriver) SaveFile(path, diff, content string) error {
	f.savedPath, f.savedDiff, f.savedContent = path, diff, content
	return f.saveErr
}

func (f *fakeDriver) Rollback(_ context.Context, res *GoalResult, step int) (bool, error) {
	f.rolledBackN = step
	if f.rollbackErr != nil {
		return f.rollbackOK, f.rollbackErr
	}
	// step itself is undone, so it goes too — same truncation as
	// resolver.Rollback. This read [:step] and so left one command more
	// than the real driver does.
	res.Commands = res.Commands[:step-1]
	return true, nil
}

func (f *fakeDriver) RecordAbort(res *GoalResult) {
	if res != nil {
		res.End = EndAborted
	}
}

func (f *fakeDriver) Tracker() []GoalStats    { return f.tracker }
func (f *fakeDriver) UsageSnapshot() Snapshot { return f.snap }

func testUIModel() Model {
	m := New(context.Background(), newFakeDriver(), SessionInfo{Proposer: "test-model"})
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	return m
}

// displayCol is where sub starts on screen, in columns, not bytes.
// Box-drawing and marker glyphs are multi-byte, so strings.Index
// alone compares two different units and quietly lies.
func displayCol(line, sub string) int {
	i := strings.Index(line, sub)
	if i < 0 {
		return -1
	}
	return lipgloss.Width(line[:i])
}

// stripANSI drops styling so a rendered line can be measured or
// matched. lipgloss v2 styles off-TTY too, so anything asserting on
// text that spans two differently-styled runs needs this — under v1
// the escapes simply weren't there to get in the way.
func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// plain is stripANSI, named for how it reads at a call site.
func plain(s string) string { return stripANSI(s) }

var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

func typeKey(s string) tea.KeyPressMsg {
	if s == "space" {
		return tea.KeyPressMsg{Code: ' ', Text: " "}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func busyUIModel() Model {
	m := testUIModel()
	m.waiting = true
	m.nav.focus = focusInput
	m.prompt.Focus()
	return m
}

func tableRow() *stepRow {
	return &stepRow{
		command: "ps aux",
		cmd: cmdState{ec: &ExecutedCommand{
			Result: Result{Stdout: "USER PID COMMAND\nroot 1 init\nmo 4821 node server.js\n"},
			Post:   &PostJudgment{FromJudge: true, Status: "clean_success", RenderKind: KindTable},
		}},
	}
}

func tableBlock() *goalBlock {
	mkrow := func(cmd, out string, kind RenderKind) *stepRow {
		return &stepRow{command: cmd, cmd: cmdState{ec: &ExecutedCommand{
			Result: Result{Stdout: out},
			Post:   &PostJudgment{FromJudge: true, Status: "clean_success", RenderKind: kind},
		}}}
	}
	steps := []*stepRow{mkrow("ps aux", "USER PID COMMAND\nroot 1 init\na 2 x\nb 3 y\nc 4 z\nd 5 w\ne 6 v\n", KindTable)}
	for range 30 {
		steps = append(steps, mkrow("echo x", "x\n", KindInline))
	}
	return &goalBlock{goal: "g", steps: steps}
}

func usageModel() (Model, *fakeDriver) {
	drv := newFakeDriver()
	drv.tracker = []GoalStats{{
		Text: "find it",
		End:  "done",
		Steps: []StepStats{{
			Command: "ls -la", Propose: 200 * time.Millisecond, Dwell: 1500 * time.Millisecond,
			Exec: 90 * time.Millisecond, ExitCode: 0, JudgePost: 120 * time.Millisecond,
			ProposerPrompt: 100, ProposerComplete: 20, JudgePrompt: 60,
			Attention: 0.2, GoalAchieved: 0.9, HasPost: true,
		}},
	}}
	drv.snap = Snapshot{Goals: 1, Commands: 1, Propose: 200 * time.Millisecond, Dwell: 1500 * time.Millisecond,
		Exec: 90 * time.Millisecond, Judge: 120 * time.Millisecond, ProposerTokens: 120, JudgeTokens: 60}
	m := New(context.Background(), drv, SessionInfo{Proposer: "test-model"})
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	return m, drv
}
