package extract

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capabilities"
	"github.com/vitzeno/detent/internal/reduce"
)

func TestDeterministicConstructor_Candidates_PID_FromGoalTextLiteral(t *testing.T) {
	tests := []struct {
		name     string
		goalText string
		wantN    int
	}{
		{name: "goal names a literal pid", goalText: "kill pid 4821", wantN: 1},
		{name: "goal has no number at all", goalText: "kill the node process", wantN: 0},
		{name: "goal names two distinct numbers, deduplicated if repeated", goalText: "kill 4821 or 4821 again", wantN: 1},
		{name: "goal names two different numbers", goalText: "kill 4821 or 5140", wantN: 2},
	}

	c := &DeterministicConstructor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates, err := c.Candidates(context.Background(), capabilities.ArgPID, tt.goalText, State{})
			require.NoError(t, err)
			assert.Len(t, candidates, tt.wantN)
			for _, cand := range candidates {
				// literal-derived candidates never carry an owner — gate
				// must refuse them without separate real process data.
				_, hasOwner := cand.Fields["owner"]
				assert.False(t, hasOwner)
			}
		})
	}
}

func TestDeterministicConstructor_Candidates_PID_StateTakesPriorityOverGoalText(t *testing.T) {
	c := &DeterministicConstructor{}
	state := State{Values: []reduce.Value{
		{Type: "pid", Value: map[string]any{"pid": 9999, "owner": "mohamed", "cmd": "node server.js"}},
	}}

	// Goal text names pid 4821 directly, but a state-derived candidate
	// (from a real unix__process_list run) must win outright, per §5.
	candidates, err := c.Candidates(context.Background(), capabilities.ArgPID, "kill pid 4821", state)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, 9999, candidates[0].Fields["pid"])
	assert.Equal(t, "mohamed", candidates[0].Fields["owner"])
	assert.Equal(t, "node server.js", candidates[0].Fields["cmd"])
}

func TestDeterministicConstructor_Candidates_PID_StateEntriesMissingFieldsAreSkipped(t *testing.T) {
	c := &DeterministicConstructor{}
	state := State{Values: []reduce.Value{
		{Type: "pid", Value: "not-a-map"},      // wrong shape entirely
		{Type: "pid", Value: map[string]any{}}, // missing pid field
		{Type: "path", Value: "/some/path"},    // wrong type, irrelevant here
	}}

	candidates, err := c.Candidates(context.Background(), capabilities.ArgPID, "kill 4821", state)
	require.NoError(t, err)
	// falls through to the goal-text literal since nothing usable was in state
	require.Len(t, candidates, 1)
	assert.Equal(t, 4821, candidates[0].Fields["pid"])
}
