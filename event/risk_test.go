package event

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The property the whole safety story rests on: a hook may add to the
// verdict and may never take away from it. It holds because Widen is
// arithmetic, not because anything remembers to check.
func TestRisk_WidenNeverNarrows(t *testing.T) {
	dangerous := Risk{Dangerous: true, Mutability: MutIrreversible, ScopeRisk: 0.9,
		Note: "rm -rf", FromJudge: true}

	softeners := []Risk{
		{},                                  // says nothing
		{Dangerous: false},                  // says not dangerous
		{Mutability: MutRead, ScopeRisk: 0}, // says harmless
		{Mutability: MutRead, ScopeRisk: -1, Note: ""}, // says unknown
	}
	for _, soft := range softeners {
		got := dangerous.Widen(soft)
		assert.True(t, got.Dangerous, "a hook cannot clear Dangerous")
		assert.Equal(t, MutIrreversible, got.Mutability, "a hook cannot lower mutability")
		assert.Equal(t, 0.9, got.ScopeRisk, "a hook cannot lower scope risk")
		assert.True(t, got.FromJudge, "a hook cannot un-judge a verdict")
		assert.Contains(t, got.Note, "rm -rf", "a hook cannot erase a note")
	}
}

func TestRisk_WidenRaises(t *testing.T) {
	tests := []struct {
		name       string
		from, with Risk
		want       Risk
	}{
		{
			name: "unknown takes whatever speaks first",
			from: UnknownRisk(),
			with: Risk{Mutability: MutWorkspace, ScopeRisk: 0.3, FromJudge: true},
			want: Risk{Mutability: MutWorkspace, ScopeRisk: 0.3, FromJudge: true},
		},
		{
			name: "the regex hook raises Dangerous on its own",
			from: Risk{Mutability: MutRead, ScopeRisk: 0.1},
			with: Risk{Dangerous: true, Note: "matched rm -rf"},
			want: Risk{Dangerous: true, Mutability: MutRead, ScopeRisk: 0.1, Note: "matched rm -rf"},
		},
		{
			name: "mutability climbs the ladder",
			from: Risk{Mutability: MutWorkspace},
			with: Risk{Mutability: MutSystem},
			want: Risk{Mutability: MutSystem},
		},
		{
			name: "notes accumulate rather than replace",
			from: Risk{Note: "first"},
			with: Risk{Note: "second"},
			want: Risk{Note: "first; second"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.from.Widen(tt.with))
		})
	}
}

// Order must not change the outcome, or the chain's answer depends on
// which hook happened to be registered first.
func TestRisk_WidenIsOrderIndependent(t *testing.T) {
	a := Risk{Dangerous: true, Note: "a"}
	b := Risk{Mutability: MutSystem, ScopeRisk: 0.7, Note: "b"}
	c := Risk{Mutability: MutRead, FromJudge: true, Note: "c"}

	fwd := UnknownRisk().Widen(a).Widen(b).Widen(c)
	rev := UnknownRisk().Widen(c).Widen(b).Widen(a)

	assert.Equal(t, fwd.Dangerous, rev.Dangerous)
	assert.Equal(t, fwd.Mutability, rev.Mutability)
	assert.Equal(t, fwd.ScopeRisk, rev.ScopeRisk)
	assert.Equal(t, fwd.FromJudge, rev.FromJudge)
}

// Parallelism is the one thing mutability still decides, now that
// checkpointing is per Turn.
func TestRisk_OnlyReadOnlyRunsAlongsideSiblings(t *testing.T) {
	assert.True(t, Risk{Mutability: MutRead}.ReadOnly())
	for _, m := range []string{"", MutWorkspace, MutSystem, MutIrreversible} {
		assert.False(t, Risk{Mutability: m}.ReadOnly(), "%q must run alone", m)
	}
}
