// Package classify provides the Judge interface and question/answer types.
package classify

import "context"

// State is the context handed to a Judge alongside Questions.
type State any

// Judge returns typed judgments; Ask batches one iteration into a single call.
type Judge interface {
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
