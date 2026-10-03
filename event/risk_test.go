package event

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The safety story rests on this: a hook may add to a verdict, never take from
// it, and it holds because Widen is arithmetic, not because anything checks.
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
		assert.InDelta(t, 0.9, got.ScopeRisk, 1e-9, "a hook cannot lower scope risk")
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
	assert.InDelta(t, fwd.ScopeRisk, rev.ScopeRisk, 1e-9)
	assert.Equal(t, fwd.FromJudge, rev.FromJudge)
}

// A tool that declared nothing must not be made read-only by a hook
// claiming so, or sed -i runs alongside its siblings.
func TestRisk_UnknownIsNotLoweredByAReadOnlyClaim(t *testing.T) {
	got := UnknownRisk().Widen(Risk{Mutability: Declared("")}).Widen(Risk{Mutability: MutRead})
	assert.Equal(t, MutUnknown, got.Mutability)
	assert.False(t, got.ReadOnly())

	got = UnknownRisk().Widen(Risk{Mutability: Declared(MutRead)})
	assert.True(t, got.ReadOnly(), "a tool declaring read-only still runs in parallel")
	assert.Equal(t, MutWorkspace, got.Widen(Risk{Mutability: MutWorkspace}).Mutability)
}

// A hook with no answer says zero by its zero value, which must not
// read as a scope the human is then shown.
func TestRisk_ScopeStaysUnknownUntilSomethingAnswers(t *testing.T) {
	tests := []struct {
		name string
		with Risk
		want float64
	}{
		{"a silent hook", Risk{}, -1},
		{"the judge answering zero", Risk{FromJudge: true}, 0},
		{"the judge with no answer", Risk{FromJudge: true, ScopeRisk: -1}, -1},
		{"a hook naming a scope", Risk{ScopeRisk: 0.4}, 0.4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.InDelta(t, tt.want, UnknownRisk().Widen(tt.with).ScopeRisk, 1e-9)
		})
	}
}

// Parallelism is the one thing mutability still decides.
func TestRisk_OnlyReadOnlyRunsAlongsideSiblings(t *testing.T) {
	assert.True(t, Risk{Mutability: MutRead}.ReadOnly())
	for _, m := range []string{"", MutUnknown, MutWorkspace, MutSystem, MutIrreversible} {
		assert.False(t, Risk{Mutability: m}.ReadOnly(), "%q must run alone", m)
	}
}
