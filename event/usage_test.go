package event

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// One Step with a cost and one without still has a cost: what was said is
// a lower bound, which is more than nothing.
func TestUsage_AddCarriesCacheAndCost(t *testing.T) {
	a := Usage{PromptTokens: 100, CachedTokens: 80, CacheWriteTokens: 5, Cost: 0.25, HasCost: true}
	b := Usage{PromptTokens: 50, CachedTokens: 40}
	got := a.Add(b)
	assert.Equal(t, 150, got.PromptTokens)
	assert.Equal(t, 120, got.CachedTokens)
	assert.Equal(t, 5, got.CacheWriteTokens)
	assert.InDelta(t, 0.25, got.Cost, 1e-12)
	assert.True(t, got.HasCost)
	assert.False(t, Usage{}.Add(Usage{}).HasCost)
}
