// Package loop is the iteration engine (PLAN.md §4): select, gate,
// execute, reduce, append, repeat — until the goal test says done, a
// budget stops it, or nothing reachable remains.
package loop

import (
	"github.com/vitzeno/detent/internal/capabilities"
	"github.com/vitzeno/detent/internal/extract"
)

// Finding is one capability's reduced output for one step — what §4.5
// calls "observed": read-only evidence for the next selection and the
// human render, never re-decided.
type Finding struct {
	Step   int            `json:"step"`
	Action string         `json:"action"` // qualified name
	Facts  map[string]any `json:"facts,omitempty"`
	Empty  bool           `json:"empty,omitempty"`
}

// ValueEntry is one typed value in State.Values (§4.5) — the flat index a
// Constructor queries, tagged with which step produced it.
type ValueEntry struct {
	Value      any `json:"value"`
	SourceStep int `json:"source_step"`
}

// Resolved is one target the Judge actually decided on via a target
// Choice, not merely observed (§4.5) — the decision log oscillation
// detection and replay-testing depend on. Not yet used to skip re-asking
// for an already-resolved slot (§4.5's "never re-decided" optimization) —
// that's deferred; this step only makes sure the log exists and is
// accurate.
type Resolved struct {
	Step                 int     `json:"step"`
	Capability           string  `json:"capability"`
	Arg                  string  `json:"arg"`
	CandidateID          string  `json:"candidate_id"`
	Value                any     `json:"value"`
	TargetResolvableNoul float64 `json:"target_resolvable_noul"`
}

// State is §4.5's model: Findings is what was observed, Values is the
// typed flat index built from Findings' reducers, Resolved is what was
// decided (as opposed to merely observed).
type State struct {
	Goal     string                  `json:"goal"`
	Findings []Finding               `json:"findings"`
	Values   map[string][]ValueEntry `json:"values"`
	Resolved []Resolved              `json:"resolved"`
}

func newState(goal string) *State {
	return &State{Goal: goal, Values: map[string][]ValueEntry{}}
}

// Reason names which termination condition stopped the run (§4.1) — a
// render can say "stopped at N steps" vs "decided it was done" rather
// than inferring the difference from what's left in State.
type Reason string

const (
	ReasonGoalAchieved      Reason = "goal_achieved"
	ReasonDone              Reason = "done_selected"
	ReasonCannotProceed     Reason = "cannot_proceed"
	ReasonGoalUnsatisfiable Reason = "goal_unsatisfiable"
	ReasonStepBudget        Reason = "step_budget"
	ReasonStateBudget       Reason = "state_budget"
	ReasonLowConfidence     Reason = "low_confidence"
	ReasonAmbiguousTarget   Reason = "ambiguous_target"
	// ReasonWriteBudget and ReasonDeclined are §4.3's two mutation-specific
	// stops: the write budget is hard and small (default 3, not a
	// performance knob), and a decline stops the *entire* run — never
	// "skip this step and carry on."
	ReasonWriteBudget Reason = "write_budget"
	ReasonDeclined    Reason = "declined"
)

// Termination is what the loop always exits with — never a silent stop
// (§4.1). Detail carries the human-readable specifics (which candidates
// were ambiguous, what confidence was seen, etc.).
type Termination struct {
	Reason Reason
	Detail string
}

// ConfirmRequest carries everything §4.3's confirm dialog needs: the run,
// not just the step. TargetDesc is the resolved, human-facing form of the
// argument about to be acted on ("pid 4821, node server.js, owned by
// mohamed"), never the goal's own wording — §4.3: "the confirm displays
// fully-resolved values, never the user's wording."
type ConfirmRequest struct {
	Goal        string
	Done        []Finding
	Capability  string
	Danger      capabilities.Danger
	TargetDesc  string
	WritesUsed  int
	WriteBudget int
	Step        int
	StepBudget  int
}

// Confirm is asked before every mutating capability executes (§4.3: every
// mutation stops, no exceptions, no accumulated-trust bypass). Returning
// false stops the entire run — never "skip this step and carry on."
type Confirm func(ConfirmRequest) bool

// AmbiguousChoice is what a step-driven caller must resolve by hand
// before Commit can run (§8.2's "Ambiguous target" screen) — built
// entirely from data already in hand (the target Choice's own
// probabilities), no second Judge call needed to resolve it. The
// synchronous Loop.Run convenience path has no human to ask, so it
// terminates with ReasonAmbiguousTarget instead; a step-driven caller
// (the TUI) calls Run.ResolveAmbiguous with the human's pick instead.
type AmbiguousChoice struct {
	ArgType       capabilities.ArgType
	ArgName       string
	Candidates    []extract.Candidate
	Probabilities map[string]float64
	Noul          float64 // the target_resolvable Noul that triggered this
}
