// Package usage tracks what a session costs: per-step latencies for
// every phase (propose, judge, human dwell, exec), token counts per
// model, and outcome counters. Everything is nil-safe: a nil Tracker,
// Goal, or Step accepts every call as a no-op, so loop code records
// unconditionally and a disabled tracker costs nothing.
package usage

import (
	"sync"
	"time"
)

// Usage is one model call's consumption.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	Latency          time.Duration
	Model            string
}

// Tokens totals both directions.
func (u Usage) Tokens() int { return u.PromptTokens + u.CompletionTokens }

// Tracker owns one session's goals. Use New, the zero value, or nil
// (every method is nil-safe).
type Tracker struct {
	mu    sync.Mutex
	goals []*Goal
}

// New returns an empty tracker.
func New() *Tracker { return &Tracker{} }

// StartGoal opens a goal; nil-safe.
func (t *Tracker) StartGoal(text string) *Goal {
	if t == nil {
		return nil
	}
	g := &Goal{Text: text, Started: time.Now()}
	t.mu.Lock()
	t.goals = append(t.goals, g)
	t.mu.Unlock()
	return g
}

// Goals returns a snapshot copy for rendering.
func (t *Tracker) Goals() []*Goal {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*Goal(nil), t.goals...)
}

// Goal is one user goal and its steps.
type Goal struct {
	mu       sync.Mutex
	Text     string
	Started  time.Time
	Ended    time.Time
	End      string
	Summary  string
	Steps    []*Step
	declined bool
}

// AddStep records a proposed command (run or declined); nil-safe.
func (g *Goal) AddStep(command string) *Step {
	if g == nil {
		return nil
	}
	s := &Step{Command: command, ExitCode: -1}
	g.mu.Lock()
	g.Steps = append(g.Steps, s)
	g.mu.Unlock()
	return s
}

// Finish closes the goal; nil-safe. declined is the only per-goal
// classification Snapshot needs — the caller (agent, which owns the
// EndReason vocabulary) decides it, rather than Snapshot re-deriving
// meaning from the raw end string this package doesn't own.
func (g *Goal) Finish(end, summary string, declined bool) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.End = end
	g.Summary = summary
	g.Ended = time.Now()
	g.declined = declined
	g.mu.Unlock()
}

// Duration is Ended-Started, or since Started when still open.
func (g *Goal) Duration() time.Duration {
	if g == nil {
		return 0
	}
	if !g.Ended.IsZero() {
		return g.Ended.Sub(g.Started)
	}
	return time.Since(g.Started)
}

// MachineTime sums propose+judge+exec, excluding human dwell.
func (g *Goal) MachineTime() time.Duration {
	if g == nil {
		return 0
	}
	var d time.Duration
	for _, s := range g.Steps {
		d += s.Propose + s.JudgePre + s.Exec + s.JudgePost
	}
	return d
}

// Step is one proposed command's measured phases.
type Step struct {
	mu               sync.Mutex
	Command          string
	Propose          time.Duration
	ProposerPrompt   int
	ProposerComplete int
	ProposerModel    string
	JudgePre         time.Duration
	Dwell            time.Duration
	Exec             time.Duration
	ExitCode         int
	OutputBytes      int
	JudgePost        time.Duration
	JudgePrompt      int
	JudgeComplete    int
	JudgeModel       string
	GoalAchieved     float64 // Jev second opinion, -1 when none
	Attention        float64 // -1 when none
	HasPost          bool
}

func (s *Step) SetPropose(u Usage) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.Propose = u.Latency
	s.ProposerPrompt = u.PromptTokens
	s.ProposerComplete = u.CompletionTokens
	s.ProposerModel = u.Model
	s.mu.Unlock()
}

func (s *Step) SetJudgePre(u Usage) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.JudgePre = u.Latency
	s.accumulateJudge(u)
	s.mu.Unlock()
}

func (s *Step) SetDwell(d time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.Dwell = d
	s.mu.Unlock()
}

func (s *Step) SetExec(d time.Duration, exitCode, outputBytes int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.Exec = d
	s.ExitCode = exitCode
	s.OutputBytes = outputBytes
	s.mu.Unlock()
}

func (s *Step) SetJudgePost(u Usage, attention, goalAchieved float64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.JudgePost = u.Latency
	s.accumulateJudge(u)
	s.Attention = attention
	s.GoalAchieved = goalAchieved
	s.HasPost = true
	s.mu.Unlock()
}

func (s *Step) accumulateJudge(u Usage) {
	s.JudgePrompt += u.PromptTokens
	s.JudgeComplete += u.CompletionTokens
	if u.Model != "" {
		s.JudgeModel = u.Model
	}
}

// Snapshot is session-wide rollups for the status bar.
type Snapshot struct {
	Goals          int
	Commands       int // steps with an exit code
	Declined       int
	Propose        time.Duration
	Judge          time.Duration
	Dwell          time.Duration
	Exec           time.Duration
	ProposerTokens int
	JudgeTokens    int
}

// Snapshot computes rollups; nil-safe.
func (t *Tracker) Snapshot() Snapshot {
	if t == nil {
		return Snapshot{}
	}
	var out Snapshot
	for _, g := range t.Goals() {
		out.Goals++
		if g.declined {
			out.Declined++
		}
		for _, s := range g.Steps {
			out.Propose += s.Propose
			out.Judge += s.JudgePre + s.JudgePost
			out.Dwell += s.Dwell
			out.Exec += s.Exec
			out.ProposerTokens += s.ProposerPrompt + s.ProposerComplete
			out.JudgeTokens += s.JudgePrompt + s.JudgeComplete
			if s.ExitCode >= 0 {
				out.Commands++
			}
		}
	}
	return out
}

// MachineTime excludes human dwell.
func (s Snapshot) MachineTime() time.Duration { return s.Propose + s.Judge + s.Exec }
