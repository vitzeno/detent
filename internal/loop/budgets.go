package loop

// Budgets bounds one run.
type Budgets struct {
	// Steps defaults to 3 via DefaultBudgets, not §4.1's originally
	// assumed 8: phase 0's noise-injection experiment found accuracy
	// collapses toward 0% within two steps of a wrong pick, across every
	// state-reduction strategy tried, with no recovery mechanism designed
	// yet (§9's status note: "Cap the step budget at 2–3 until recovery
	// is designed").
	Steps int
	// StateTokens is §4.5's working budget (8k, far below Jev's 32k
	// ceiling), enforced as a termination condition, not a truncation:
	// hitting it means the reducers are too permissive, not that the
	// budget should rise.
	StateTokens int
	// Writes is §4.3's hard, small mutation budget (default 3) — separate
	// from and much tighter than Steps. Not a performance knob: "the
	// ceiling stays low until §10 says otherwise."
	Writes int
}

// DefaultBudgets matches §9's status note recommendation for Steps, and
// §4.3's default for Writes.
var DefaultBudgets = Budgets{Steps: 3, StateTokens: 8000, Writes: 3}

const (
	// NextActionConfidenceFloor is §7 Layer 3's tested floor: a
	// retrospective threshold scan found this catches 95% (in-sample) /
	// 84% (held-out) of runs that would otherwise collapse to zero
	// accuracy, at the cost of stopping 28% of runs that would have been
	// fine (Youden's J = 0.69 / 0.49). Phase 0 never executed anything,
	// so this is a measured starting point, not a guarantee under real
	// execution.
	NextActionConfidenceFloor = 0.8

	// GoalAchievedFloor is untuned — deliberately set well above the
	// natural 0.5 boundary because §10 names false "complete" as the
	// dangerous failure mode (a run ending before the goal is met).
	// Better to burn step budget on a false "incomplete" than stop early.
	GoalAchievedFloor = 0.85

	// GoalSatisfiableFloor is untuned — phase 0 validated that a
	// perfectly-separating threshold exists for this Noul (J = 1.00) but
	// did not fix this exact number in the plan; 0.5 is the natural
	// decision boundary for a yes/no Noul.
	GoalSatisfiableFloor = 0.5

	// TargetResolvableFloor mirrors step 6's untuned default, same
	// reasoning: phase 0b validated J = 1.00 is achievable at *some*
	// threshold, not necessarily this one.
	TargetResolvableFloor = 0.5
)
