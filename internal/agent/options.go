package agent

import "github.com/vitzeno/detent/internal/usage"

// Option configures a Session. Only set what differs: no Judge, no
// cap, and no tracking unless asked.
type Option func(*Session)

func WithRunners(sel RunnerSelector) Option {
	return func(s *Session) { s.Runners = sel }
}

// WithID overrides the randomly generated Session.ID, e.g. so a caller
// can correlate it with a sandbox Runner's own backing resources.
func WithID(id string) Option {
	return func(s *Session) { s.ID = id }
}

func WithJudge(judge Judge) Option {
	return func(s *Session) { s.Judge = judge }
}

// WithSummarizer condenses the transcript when it outgrows its budget.
// Without one, compaction drops the oldest turns instead.
func WithSummarizer(sum Summarizer) Option {
	return func(s *Session) { s.Summarizer = sum }
}

func WithRiskThreshold(t float64) Option {
	return func(s *Session) { s.RiskThreshold = t }
}

func WithStepBudget(n int) Option {
	return func(s *Session) { s.StepBudget = n }
}

func WithStats(t *usage.Tracker) Option {
	return func(s *Session) { s.Stats = t }
}

// New builds a session around a proposer and confirmer.
func New(proposer Proposer, confirm Confirmer, opts ...Option) *Session {
	s := &Session{ID: NewSessionID(), Proposer: proposer, Confirm: confirm}
	for _, opt := range opts {
		opt(s)
	}
	return s
}
