package extract

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capabilities"
	"github.com/vitzeno/detent/internal/reduce"
)

// bigProcessState builds a state.Values["pid"] list mimicking what a real,
// busy machine produces after reduce.ProcessLines' own cap — this is the
// scenario a live run caught: without goal-text relevance filtering here,
// every one of these would be offered as a pid_target Choice option,
// comfortably risking TypeSafe's 255-option ceiling and being useless to
// a human or a model regardless.
func bigProcessState(n int, needle string, needlePID int) State {
	var values []reduce.Value
	for i := range n {
		values = append(values, reduce.Value{Type: "pid", Value: map[string]any{
			"pid": 2000 + i, "owner": "alice", "cmd": "some_background_helper",
		}})
	}
	values = append(values, reduce.Value{Type: "pid", Value: map[string]any{
		"pid": needlePID, "owner": "alice", "cmd": needle,
	}})
	return State{Values: values}
}

func TestDeterministicConstructor_Candidates_PID_RelevanceFiltering(t *testing.T) {
	c := &DeterministicConstructor{}

	t.Run("goal names the process, only matches offered", func(t *testing.T) {
		state := bigProcessState(60, "node server.js", 4821)
		candidates, err := c.Candidates(context.Background(), capabilities.ArgPID, "kill the node process", state)
		require.NoError(t, err)
		require.Len(t, candidates, 1)
		assert.Equal(t, 4821, candidates[0].Fields["pid"])
	})

	t.Run("goal matches nothing by name, everything survives capped", func(t *testing.T) {
		state := bigProcessState(60, "node server.js", 4821)
		candidates, err := c.Candidates(context.Background(), capabilities.ArgPID,
			"kill whatever is using the most memory", state)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(candidates), MaxCandidates)
		assert.Len(t, candidates, MaxCandidates) // 61 total > MaxCandidates(50)
	})
}
