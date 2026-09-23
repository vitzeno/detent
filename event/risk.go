package event

// Risk is what the hook chain decided about one Call, before it runs.
type Risk struct {
	// Dangerous is the only field that gates anything: true means a
	// human sees the Call and must approve it.
	Dangerous bool
	// Mutability is what the Call would change: read_only,
	// writes_workspace, system_affecting, likely_irreversible.
	Mutability string
	// ScopeRisk is how far the blast radius reaches, 0 to 1. -1 when
	// nothing answered.
	ScopeRisk float64
	Note      string
	// FromJudge is false when only the cheap hooks spoke, so the UI
	// never presents a heuristic as a verdict.
	FromJudge bool
}

// UnknownRisk is the starting point of every chain: nothing known,
// nothing claimed.
func UnknownRisk() Risk { return Risk{ScopeRisk: -1, Mutability: ""} }

// Widen folds one hook's answer into what the chain already had. It
// only ever adds: Dangerous ORs, ScopeRisk takes the max, and a
// Mutability may move up the ladder but never down.
//
// This is why a hook cannot soften a confirm even if it returns one
// saying so. The rule is arithmetic, not a convention a test has to
// remember to check.
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

// ReadOnly reports whether this Call may run alongside its siblings.
// Anything less certain than read_only runs alone.
func (r Risk) ReadOnly() bool { return r.Mutability == MutRead }

// The mutability ladder, least to most. Ordered, because Widen moves
// up it and a string comparison would not.
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
	return 0 // unknown, so anything real outranks it
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
