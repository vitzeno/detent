// Package classify holds the Judge interface (PLAN.md §6) and its
// reference implementation, the Jev adapter (§6.1). Everything upstream —
// eventually loop, gate, tui — depends only on the types in this file,
// never on JevJudge directly: swapping the model behind Jev, or adding a
// second System One provider, is a new file in this package plus a
// one-line change at startup, nothing else in the tree touched.
package classify

import "context"

// State is the JSON-shaped context handed to a Judge alongside Questions.
// Deliberately untyped (TypeSafe's own `state` field is string|object|array)
// — this package doesn't need to know the full §4.5 state model before the
// loop (step 7) actually builds one. Single-shot classification (step 5)
// only ever needs a goal string.
type State any

// Judge is a System One model: it returns typed judgments over a closed
// set of options, never free text, never a command. Ask batches every
// question for one loop iteration into a single call — batching is
// load-bearing (SPIKE.md's own reasoning: two calls measure a different,
// untested loop), so an adapter must not split this into multiple
// requests internally.
type Judge interface {
	Ask(ctx context.Context, state State, questions Questions) (Answers, Usage, error)
}

// Questions batches every question for one iteration; map key is the
// question id, meaningful only to the caller (never sent as a semantic
// signal to the model beyond being the key TypeSafe echoes back in answers).
type Questions map[string]Question

// Question is a closed sum type: exactly one of Choice, Noul, or Score is
// set. ToWireQuestion (jev.go) rejects a Question with none set, since a
// zero-value Question would otherwise silently become an ambiguous request.
type Question struct {
	Instructions string
	Choice       *ChoiceQuestion
	Noul         *NoulQuestion
	Score        *ScoreQuestion
}

// ChoiceQuestion picks one of a defined set. Criteria maps an option id to
// its description — the id is what comes back in Answer.Choice.
//
// A value is a plain string, or a structured object (e.g.
// {"what": ..., "not_for": ..., "examples": [...]}) — phase 0's validated
// spike (experiments/scripts/spike.py's CAPABILITIES/TERMINALS dicts)
// sent criteria as structured JSON objects for confusable options, not
// flattened strings, and that structure is part of what four rounds of
// wording iteration actually validated. any lets a caller send either.
type ChoiceQuestion struct {
	Criteria map[string]any
}

// NoulQuestion asks the probability that a condition holds. No criteria
// field yet: every Noul question in the validated v1 design (goal_achieved,
// goal_satisfiable, *_target_resolvable) uses plain yes/no semantics from
// Instructions alone. TypeSafe's API does allow overriding the true/false
// descriptions via an optional criteria map — add it here if a real
// question ever needs that, rather than speculatively now.
type NoulQuestion struct{}

// ScoreQuestion is a graded position on ordered levels. Levels is an
// ordered slice — index is the level id — matching TypeSafe's own
// `criteria` shape for score questions (an ordered array, 2-10 entries),
// not PLAN.md §6's original map[string]string sketch: the API scores a
// probability-weighted mean of level *index*, so a map would need a
// fragile numeric-string-key convention to reconstruct that order.
// Confirmed against the live API docs while building this adapter.
type ScoreQuestion struct {
	Levels []string
}

// Answers holds one Answer per question id, same keys as the Questions
// that were asked.
type Answers map[string]Answer

// Answer is a discriminated-by-caller union: which fields are meaningful
// depends on which of Choice/Noul/Score the matching Question set, exactly
// as Question itself is a closed sum type on the way in.
type Answer struct {
	Choice        string             // set for a Choice answer
	Confidence    float64            // Choice/Score only — never for Noul
	Probabilities map[string]float64 // Choice/Score only, sums to 1.0
	Noul          float64            // set for a Noul answer, in [0, 1]
	Score         float64            // set for a Score answer
	Legend        map[string]string  // Score only: level index (as string) -> description
}

// Usage is required from every adapter, not optional telemetry — the
// regression suite and the gate's confidence floor (§7) both depend on
// these fields existing regardless of provider.
type Usage struct {
	InputTokens, OutputTokens int
	LatencyMS                 float64
	Model                     string // the adapter's *resolved* model id, not an alias
}
