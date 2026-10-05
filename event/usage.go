package event

import "time"

// Usage is what one model call cost. Here rather than its own package
// because StepEnded carries it and this one may import neither.
type Usage struct {
	PromptTokens     int           `json:"PromptTokens"`
	CompletionTokens int           `json:"CompletionTokens"`
	Latency          time.Duration `json:"Latency"`
	Model            string        `json:"Model"`
	// CachedTokens is the part of PromptTokens served from the endpoint's
	// cache, and CacheWriteTokens what it stored, where it bills writes.
	CachedTokens     int `json:"CachedTokens"`
	CacheWriteTokens int `json:"CacheWriteTokens"`
	// Cost is the endpoint's price in USD, meaningful only with HasCost:
	// an endpoint that says nothing is unknown, not free.
	Cost    float64 `json:"Cost"`
	HasCost bool    `json:"HasCost"`
}

// Tokens is what a budget and a status bar both want.
func (u Usage) Tokens() int { return u.PromptTokens + u.CompletionTokens }

// Add accumulates, for a Step that asked twice.
func (u Usage) Add(o Usage) Usage {
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
	u.Latency += o.Latency
	u.CachedTokens += o.CachedTokens
	u.CacheWriteTokens += o.CacheWriteTokens
	u.Cost += o.Cost
	u.HasCost = u.HasCost || o.HasCost
	if o.Model != "" {
		u.Model = o.Model
	}
	return u
}
