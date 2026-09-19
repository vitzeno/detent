// Package extract builds candidate lists for a capability's arguments
// (PLAN.md §5, §5.1) — deterministic code by default, run before every
// Judge call because a Judge needs candidates as Choice criteria.
package extract

import (
	"context"

	"github.com/vitzeno/detent/internal/capabilities"
	"github.com/vitzeno/detent/internal/reduce"
)

// MaxCandidates caps how many candidates a Constructor may return for one
// type in one call (§5): the action catalog won't blow the token budget,
// but an unbounded path match over a large repo or a full process list on
// a busy box will.
const MaxCandidates = 50

// Candidate is a deterministic proposal, never a decision (§5.1). ID
// becomes a Judge Choice option id; Desc becomes that option's criteria
// text — phase 0b's exact shape; Fields carries the structured value(s) a
// handler eventually needs once a candidate is chosen.
type Candidate struct {
	ID     string
	Desc   string
	Fields map[string]any
}

// State is the context a Constructor can draw candidates from. Values
// mirrors §4.5's typed flat index — a narrowed stand-in until the real
// state model lands in step 7. From iteration two onward, facts already
// in state are the first source of candidates (§5), ahead of anything
// extracted from the goal text.
type State struct {
	Values []reduce.Value
}

// Constructor proposes. It never decides. This is enforced by the return
// type, not a rule someone has to remember: Candidates can only return a
// list, never a single resolved value (§5.1). Only a Judge's target
// Choice, checked against its paired target_resolvable Noul (§7), selects
// among what a Constructor proposes.
type Constructor interface {
	Candidates(ctx context.Context, argType capabilities.ArgType, goalText string, state State) ([]Candidate, error)
}
