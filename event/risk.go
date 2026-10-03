package event

// Risk is what the hook chain decided about one tool call, before it runs.
type Risk struct {
	// Dangerous is the only field that gates anything.
	Dangerous bool
	// Mutability is one of the Mut* ladder below, or "" for no opinion.
	Mutability string
	// ScopeRisk is blast radius, 0 to 1, or -1 when nothing answered.
	// A zero from a hook that is not the judge is no answer.
	ScopeRisk float64
	// Note is harness text. Never quote the command in it: the log withholds that.
	Note string
	// FromJudge is false when only the cheap hooks spoke.
	FromJudge bool
}

// UnknownRisk starts every chain.
func UnknownRisk() Risk { return Risk{ScopeRisk: -1} }

// Widen folds one hook's answer in, and only ever adds: Dangerous ORs,
// ScopeRisk maxes, Mutability climbs. A hook cannot soften a confirm
// even if it returns one saying so.
func (r Risk) Widen(o Risk) Risk {
	r.Dangerous = r.Dangerous || o.Dangerous
	r.FromJudge = r.FromJudge || o.FromJudge
	if answered := o.ScopeRisk > 0 || o.FromJudge; answered && o.ScopeRisk > r.ScopeRisk {
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

// Declared is a tool's own mutability as the chain reads it. A tool
// that declares none is unknown, which no read-only claim can lower.
func Declared(m string) string {
	if m == "" {
		return MutUnknown
	}
	return m
}

// The mutability ladder, least to most. Widen climbs it. Unknown sits
// above read-only, so a hook calling bash read-only cannot parallelise it.
const (
	MutRead         = "read_only"
	MutUnknown      = "unknown"
	MutWorkspace    = "writes_workspace"
	MutSystem       = "system_affecting"
	MutIrreversible = "likely_irreversible"
)

func rank(m string) int {
	switch m {
	case MutRead:
		return 1
	case MutUnknown:
		return 2
	case MutWorkspace:
		return 3
	case MutSystem:
		return 4
	case MutIrreversible:
		return 5
	}
	return 0 // no opinion, so anything real outranks it
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
