package classify

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
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

// What the safety argument rests on: a failure is an error the engine
// can show, and never a verdict that narrows anything.
func TestRiskJudge_Assess(t *testing.T) {
	for _, tc := range []struct {
		name      string
		asker     Asker
		field     float64
		arg       float64
		wantErr   bool
		dangerous bool
		mut       string
		scope     float64
	}{
		{name: "a failed request is an error", asker: failing{errors.New("HTTP 401")}, wantErr: true},
		{name: "read only is not dangerous", asker: answers(event.MutRead, 0.1), mut: event.MutRead, scope: 0.1},
		{name: "workspace is not dangerous", asker: answers(event.MutWorkspace, 0.1), mut: event.MutWorkspace, scope: 0.1},
		{name: "system is dangerous", asker: answers(event.MutSystem, 0.1), dangerous: true, mut: event.MutSystem, scope: 0.1},
		{name: "irreversible is dangerous", asker: answers(event.MutIrreversible, 0.1), dangerous: true, mut: event.MutIrreversible, scope: 0.1},
		{name: "the argument beats the field", asker: answers(event.MutRead, 0.3), field: 0.9, arg: 0.2, dangerous: true, mut: event.MutRead, scope: 0.3},
		{name: "the field beats the default", asker: answers(event.MutRead, 0.3), field: 0.2, dangerous: true, mut: event.MutRead, scope: 0.3},
		{name: "no scope answer stays unknown", asker: fixed{"mutability": {Choice: event.MutRead}}, mut: event.MutRead, scope: -1},
		{name: "no asker adds nothing", asker: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			risk, err := RiskJudge{Asker: tc.asker, Threshold: tc.field}.Assess(context.Background(), "cmd", tc.arg)
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, event.Risk{}, risk, "a failure is no answer, which Widen folds in as nothing")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.dangerous, risk.Dangerous)
			if tc.asker != nil {
				assert.Equal(t, tc.mut, risk.Mutability)
				assert.InDelta(t, tc.scope, risk.ScopeRisk, 1e-9)
			}
		})
	}
}

// Every tool call waits on this, so a stalled endpoint is given up on soon,
// and says so rather than passing for a quiet verdict.
func TestRiskJudge_GivesUpOnAStalledJudge(t *testing.T) {
	start := time.Now()
	_, err := RiskJudge{Asker: stalled{}, Timeout: 20 * time.Millisecond}.Assess(context.Background(), "ls", 0)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 2*time.Second)
}

type scopeRisk float64

func (s scopeRisk) Ask(context.Context, State, Questions) (Answers, Usage, error) {
	return Answers{"mutability": {Choice: "read_only"}, "scope_risk": {Noul: float64(s)}}, Usage{}, nil
}

type fixed Answers

func (f fixed) Ask(context.Context, State, Questions) (Answers, Usage, error) {
	return Answers(f), Usage{}, nil
}

func answers(mutability string, scope float64) fixed {
	return fixed{"mutability": {Choice: mutability}, "scope_risk": {Noul: scope}}
}

type failing struct{ err error }

func (f failing) Ask(context.Context, State, Questions) (Answers, Usage, error) {
	return nil, Usage{}, f.err
}

type stalled struct{}

func (stalled) Ask(ctx context.Context, _ State, _ Questions) (Answers, Usage, error) {
	<-ctx.Done()
	return nil, Usage{}, ctx.Err()
}
