package event

import "time"

// Usage is what one model call cost. It lives here rather than in a
// package of its own because StepEnded carries it and this package may
// not import one that does.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	Latency          time.Duration
	Model            string
}

// Tokens is the total, which is what a budget and a status bar both want.
func (u Usage) Tokens() int { return u.PromptTokens + u.CompletionTokens }

// Add accumulates, for a Step that had to ask twice.
func (u Usage) Add(o Usage) Usage {
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
	u.Latency += o.Latency
	if o.Model != "" {
		u.Model = o.Model
	}
	return u
}
