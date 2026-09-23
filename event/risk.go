package event

// Risk is what the hook chain decided about one Call, before it runs.
type Risk struct {
	// Dangerous is the only field that gates anything.
	Dangerous bool
	// Mutability is one of the Mut* ladder below.
	Mutability string
	// ScopeRisk is blast radius, 0 to 1; -1 when nothing answered.
	ScopeRisk float64
	Note      string
	// FromJudge is false when only the cheap hooks spoke.
	FromJudge bool
}

// UnknownRisk starts every chain.
func UnknownRisk() Risk { return Risk{ScopeRisk: -1, Mutability: ""} }

// Widen folds one hook's answer in, and only ever adds: Dangerous ORs,
// ScopeRisk maxes, Mutability climbs. A hook cannot soften a confirm
// even if it returns one saying so.
func (r Risk) Widen(o Risk) Risk {
	r.Dangerous = r.Dangerous || o.Dangerous
	r.FromJudge = r.FromJudge || o.FromJudge
	if o.ScopeRisk > r.ScopeRisk {
		r.ScopeRisk = o.ScopeRisk
	}
	if rank(o.Mutability) > rank(r.Mutability) {
		r.Mutability = o.Mutability
	}
	r.Note = joinNote(r.Note, o.Note)
	return r
}

// ReadOnly is the one thing mutability still decides: parallelism.
func (r Risk) ReadOnly() bool { return r.Mutability == MutRead }

// The mutability ladder, least to most. Widen climbs it.
const (
	MutRead         = "read_only"
	MutWorkspace    = "writes_workspace"
	MutSystem       = "system_affecting"
	MutIrreversible = "likely_irreversible"
)

func rank(m string) int {
	switch m {
	case MutRead:
		return 1
	case MutWorkspace:
		return 2
	case MutSystem:
		return 3
	case MutIrreversible:
		return 4
	}
	return 0 // unknown; anything real outranks it
}

func joinNote(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}
