// Package classify provides question/answer vocabulary types plus
// JevJudge, an adapter to TypeSafe's Jev. No Judge interface lives
// here — each consumer (agent, probe) declares its own at the call
// site, and JevJudge satisfies them structurally.
package classify

import (
	"context"
	"time"
)

// State is the context handed to a Judge alongside Questions.
type State any

// Asker is the shape AskOrFallback needs. Exported so a package
// outside this one can hold one; a caller just passes anything with
// an Ask method.
type Asker interface {
	Ask(ctx context.Context, state State, questions Questions) (Answers, Usage, error)
}

// Questions batches one iteration; map key is the question id.
type Questions map[string]Question

// Question sets exactly one of Choice, Noul, or Score.
type Question struct {
	Instructions string
	Choice       *ChoiceQuestion
	Noul         *NoulQuestion
	Score        *ScoreQuestion
}

// ChoiceQuestion picks one of Criteria by id.
type ChoiceQuestion struct {
	Criteria map[string]any
}

// NoulQuestion asks the probability that a condition holds.
type NoulQuestion struct{}

// ScoreQuestion is a graded position on ordered levels; index is the level id.
// Levels is a slice because the API scores a mean weighted by level index.
type ScoreQuestion struct {
	Levels []string
}

// Answers holds one Answer per question id.
type Answers map[string]Answer

// Answer holds the response fields for the matching Question kind.
type Answer struct {
	Choice        string
	Confidence    float64 // Choice/Score only; never for Noul.
	Probabilities map[string]float64
	Noul          float64
	Score         float64
	Legend        map[string]string
}

// Usage reports tokens, latency, and resolved model id.
type Usage struct {
	InputTokens, OutputTokens int
	LatencyMS                 float64
	Model                     string
}

// AskOrFallback calls judge.Ask, reporting ok=false when judge is nil
// or the call errors, so every caller's fallback collapses to one
// branch. LatencyMS is wall time measured here, not whatever the
// adapter reports, so timings are directly comparable across callers.
func AskOrFallback(ctx context.Context, j Asker, state State, questions Questions) (Answers, Usage, bool) {
	if j == nil {
		return nil, Usage{}, false
	}
	t0 := time.Now()
	answers, u, err := j.Ask(ctx, state, questions)
	if err != nil {
		return nil, Usage{}, false
	}
	u.LatencyMS = float64(time.Since(t0).Microseconds()) / 1000.0
	return answers, u, true
}
