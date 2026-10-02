package classify

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRiskJudge_ZeroThresholdTakesTheDefault(t *testing.T) {
	for _, tc := range []struct {
		noul      float64
		dangerous bool
	}{{0.2, false}, {0.5, true}, {0.9, true}} {
		j := RiskJudge{Asker: scopeRisk(tc.noul)}
		risk, err := j.Assess(context.Background(), "ls", 0)
		require.NoError(t, err)
		assert.Equal(t, tc.dangerous, risk.Dangerous, "noul %.1f", tc.noul)
	}
}

type scopeRisk float64

func (s scopeRisk) Ask(context.Context, State, Questions) (Answers, Usage, error) {
	return Answers{"mutability": {Choice: "read_only"}, "scope_risk": {Noul: float64(s)}}, Usage{}, nil
}
