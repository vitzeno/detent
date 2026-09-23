package event

import "time"

// Usage is what one model call cost. Here rather than its own package
// because StepEnded carries it and this one may import neither.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	Latency          time.Duration
	Model            string
}

// Tokens is what a budget and a status bar both want.
func (u Usage) Tokens() int { return u.PromptTokens + u.CompletionTokens }

// Add accumulates, for a Step that asked twice.
func (u Usage) Add(o Usage) Usage {
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
	u.Latency += o.Latency
	if o.Model != "" {
		u.Model = o.Model
	}
	return u
}
