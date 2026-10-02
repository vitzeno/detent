// Package classify holds the question and answer vocabulary plus
// JevJudge, an adapter to TypeSafe's Jev. Each consumer declares its own
// Judge interface, and JevJudge satisfies them structurally.
package classify

import (
	"context"
	"time"
)

// State is the context handed to a Judge alongside Questions.
type State any

// Asker is the shape AskOrFallback needs, exported so other packages can hold one.
type Asker interface {
	Ask(ctx context.Context, state State, questions Questions) (Answers, Usage, error)
}

// Questions batches one request, keyed by question id.
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

// ScoreQuestion is a graded position on ordered levels, each identified
// by its index. The API scores a mean weighted by that index.
type ScoreQuestion struct {
	Levels []string
}

// Answers holds one Answer per question id.
type Answers map[string]Answer

// Answer holds the response fields for the matching Question kind.
type Answer struct {
	Choice        string
	Confidence    float64 // Choice and Score only, never Noul.
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

// AskOrFallback calls j.Ask, reporting ok=false when j is nil or errors so a
// caller's fallback is one branch. LatencyMS is wall time measured here.
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
