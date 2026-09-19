// Package reduce turns one capability's raw output into decision-relevant
// facts (PLAN.md §4.4) — deterministic code, no Jev involved. This is
// Layer 1 of §4.4's two-layer design; Layer 2 (semantic reduction, scoring
// unstructured text against the goal) needs a Jev call and isn't built
// until a capability's output is unstructured enough to need it.
package reduce

// Value is one typed value a reducer found in a capability's output, keyed
// by the same produces/args vocabulary as capabilities.ArgType ("path",
// "pid", ...) so state.values (§4.5) can be built with one uniform shape
// regardless of which reducer produced the value.
type Value struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

// Result is one reducer's output for one capability run.
//
// Facts is the human/next-selection-facing structured summary, and
// deliberately `map[string]any` rather than a fixed struct — §4.4 is
// explicit that forcing a common shape across every capability's output
// just reintroduces the distractor problem reduction exists to solve.
//
// Empty records explicitly that the capability ran and produced nothing
// relevant, distinct from a reducer never having run at all (§4.4:
// "record empty results explicitly ... silently appending nothing is
// indistinguishable from the step never running").
type Result struct {
	Facts  map[string]any `json:"facts,omitempty"`
	Values []Value        `json:"values,omitempty"`
	Empty  bool           `json:"empty,omitempty"`
}

// Reducer is a registered, deterministic, per-capability function: raw
// output in, a Result out. Golden-file tested with no model involved
// (§4.4, §9 step 4) — that's what makes a reducer bug distinguishable from
// a classification bug once the loop exists.
type Reducer func(output string) Result
