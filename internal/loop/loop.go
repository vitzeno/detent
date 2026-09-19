package loop

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vitzeno/detent/internal/capabilities"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/exec"
	"github.com/vitzeno/detent/internal/extract"
	"github.com/vitzeno/detent/internal/gate"
	"github.com/vitzeno/detent/internal/reduce"
)

// Loop holds everything a Run needs but doesn't change between runs (§4):
// the registries, the providers, budgets, gate rules. Depends only on
// classify.Judge and extract.Constructor — interfaces, never a concrete
// provider type (§2's critical seam).
type Loop struct {
	Capabilities *capabilities.Registry
	Exec         *exec.Registry
	Reduce       *reduce.Registry
	Judge        classify.Judge
	Constructor  extract.Constructor
	Budgets      Budgets
	PathRules    gate.PathRules

	// CurrentUser is checked by gate.ValidatePID (§7: "reject processes
	// not owned by the current user") — supplied by the caller rather than
	// read from the OS here, so tests can control it directly.
	CurrentUser string

	// Confirm is Loop.Run's synchronous convenience path only (below) — a
	// blocking callback asked inline whenever a mutation needs approval.
	// A step-driven caller (the TUI, §9) doesn't use this at all; it calls
	// NewRun/Prepare/Commit directly and renders its own confirm state,
	// since a blocking callback can't coexist with an async, message-
	// driven UI that needs to redraw between steps.
	Confirm Confirm
}

// NewRun starts one execution against a goal, driven one step at a time
// via Prepare/Commit/Decline — the shape an async, message-driven caller
// (a TUI) needs: each step must render before the next begins, and a
// mutation must pause for a real interactive decision, not a blocking call.
func (l *Loop) NewRun(goal string) *Run {
	return &Run{loop: l, state: newState(goal), visited: map[string]bool{}}
}

// Run is one in-progress or finished execution. Holds exactly the mutable
// bookkeeping loop.Run's old for-loop used to keep on the stack (state,
// visited-set, writes spent, step count) — now addressable across
// separate Prepare/Commit calls instead of a single blocking one.
type Run struct {
	loop       *Loop
	state      *State
	visited    map[string]bool
	writesUsed int
	step       int
}

// State is the run's accumulated state so far — safe to read at any
// point, including mid-run (e.g. to render "done so far" while paused on
// a confirm).
func (r *Run) State() *State { return r.state }

// WritesUsed reports how many approved mutations this run has committed
// so far — for a step-driven caller (the TUI) rendering the write budget
// live, the same count ConfirmRequest.WritesUsed carries at ask time.
func (r *Run) WritesUsed() int { return r.writesUsed }

// StepsUsed reports how many iterations this run has started — not the
// same as len(State().Findings): a step that turned out ambiguous or was
// declined still consumed a step even though nothing was committed. This
// is what Prepare actually checks the step budget against.
func (r *Run) StepsUsed() int { return r.step }

// Prepared is what Prepare returns when the run should continue.
//
// When Ambiguous is set, nothing else is: Args/TargetDesc/Reducer/Confirm
// aren't known yet — the caller must resolve the target by hand (a human
// pick, §8.2) via Run.ResolveAmbiguous before Commit is possible.
//
// Otherwise Prepared is a capability ready to execute with its resolved
// args, plus — only when this capability mutates — the confirm request
// the caller must resolve before calling Commit. Confirm is nil for safe
// capabilities: nothing to approve, Commit can be called immediately.
type Prepared struct {
	Capability string
	Args       exec.Args
	TargetDesc string
	Reducer    string
	Confirm    *ConfirmRequest
	Ambiguous  *AmbiguousChoice

	// Confidence and GoalAchievedNoul are this iteration's next_action/
	// goal_achieved answers, exposed even when the run continues — a
	// step-driven caller can render "the goal-test probability as it
	// moves" (§8.1) without waiting for a Termination, which only carries
	// this once the run stops.
	Confidence       float64
	GoalAchievedNoul float64
}

// Prepare runs one iteration's selection: budgets, the batched Judge
// call, goal_achieved/goal_satisfiable, argument resolution and gating —
// everything up to but not including execution. Returns a non-nil
// Termination when the run is over; otherwise a Prepared step for the
// caller to Commit (after approval, if Prepared.Confirm is set).
func (r *Run) Prepare(ctx context.Context) (*Prepared, *Termination, error) {
	l := r.loop
	r.step++

	if r.step > l.Budgets.Steps {
		return nil, &Termination{
			Reason: ReasonStepBudget,
			Detail: fmt.Sprintf("stopped at %d steps", l.Budgets.Steps),
		}, nil
	}
	if n := stateTokens(r.state); n > l.Budgets.StateTokens {
		return nil, &Termination{
			Reason: ReasonStateBudget,
			Detail: fmt.Sprintf("state reached ~%d tokens (budget %d)", n, l.Budgets.StateTokens),
		}, nil
	}

	extractState := extract.State{Values: stateValuesForExtraction(r.state)}
	pathCandidates, err := l.Constructor.Candidates(ctx, capabilities.ArgPath, r.state.Goal, extractState)
	if err != nil {
		return nil, nil, err
	}
	pidCandidates, err := l.Constructor.Candidates(ctx, capabilities.ArgPID, r.state.Goal, extractState)
	if err != nil {
		return nil, nil, err
	}
	candidatesByType := map[capabilities.ArgType][]extract.Candidate{
		capabilities.ArgPath: pathCandidates,
		capabilities.ArgPID:  pidCandidates,
	}

	criteria, offered := reachableCriteria(l.Capabilities, r.visited, candidatesByType)
	questions := buildQuestions(l.Capabilities, criteria, offered)

	answers, _, err := l.Judge.Ask(ctx, classify.State(stateForJudge(r.state)), questions)
	if err != nil {
		return nil, nil, err
	}

	if achieved := answers["goal_achieved"]; achieved.Noul >= GoalAchievedFloor {
		return nil, &Termination{Reason: ReasonGoalAchieved, Detail: fmt.Sprintf("noul %.2f", achieved.Noul)}, nil
	}
	if satisfiable := answers["goal_satisfiable"]; satisfiable.Noul < GoalSatisfiableFloor {
		return nil, &Termination{Reason: ReasonGoalUnsatisfiable, Detail: fmt.Sprintf("noul %.2f", satisfiable.Noul)}, nil
	}

	next := answers["next_action"]
	if next.Confidence < NextActionConfidenceFloor {
		return nil, &Termination{
			Reason: ReasonLowConfidence,
			Detail: fmt.Sprintf("next_action confidence %.2f on %q", next.Confidence, next.Choice),
		}, nil
	}

	switch next.Choice {
	case "done":
		return nil, &Termination{Reason: ReasonDone}, nil
	case "cannot_proceed":
		return nil, &Termination{Reason: ReasonCannotProceed}, nil
	}

	reg, ok := l.Capabilities.Get(next.Choice)
	if !ok {
		return nil, nil, fmt.Errorf("loop: next_action chose %q, not a known capability", next.Choice)
	}
	isMutation := reg.Action.Danger != capabilities.DangerSafe

	// Write budget is checked before spending any effort resolving args
	// (which can involve a real gate check, e.g. gate.ValidatePID) — a
	// cheap, capability-agnostic stop that doesn't need to know anything
	// about this capability's arguments yet.
	if isMutation && r.writesUsed >= l.Budgets.Writes {
		return nil, &Termination{
			Reason: ReasonWriteBudget,
			Detail: fmt.Sprintf("stopped at %d writes", l.Budgets.Writes),
		}, nil
	}

	args := exec.Args{}
	targetDesc := reg.Action.Description
	if required := requiredArgs(reg.Action); len(required) == 1 {
		arg := required[0]
		resolved, desc, ambiguous, err := l.resolveArg(r.state, next.Choice, arg, answers, offered[arg.Type], r.step)
		if err != nil {
			return nil, nil, err
		}
		if ambiguous != nil {
			return &Prepared{
				Capability: next.Choice, Ambiguous: ambiguous,
				Confidence: next.Confidence, GoalAchievedNoul: answers["goal_achieved"].Noul,
			}, nil, nil
		}
		args = exec.Args{arg.Name: resolved}
		targetDesc = desc
	}

	prepared := &Prepared{
		Capability:       next.Choice,
		Args:             args,
		TargetDesc:       targetDesc,
		Reducer:          reg.Action.Reducer,
		Confidence:       next.Confidence,
		GoalAchievedNoul: answers["goal_achieved"].Noul,
	}
	if isMutation {
		prepared.Confirm = &ConfirmRequest{
			Goal:        r.state.Goal,
			Done:        append([]Finding{}, r.state.Findings...),
			Capability:  next.Choice,
			Danger:      reg.Action.Danger,
			TargetDesc:  targetDesc,
			WritesUsed:  r.writesUsed,
			WriteBudget: l.Budgets.Writes,
			Step:        r.step,
			StepBudget:  l.Budgets.Steps,
		}
	}
	return prepared, nil, nil
}

// Commit executes a Prepared step: dispatch, reduce, append to state,
// mark visited. Call only after an unconditional Prepared step (Confirm
// nil) or one whose confirm was approved — never after a decline (call
// Decline instead, which records the stop without executing).
func (r *Run) Commit(ctx context.Context, p *Prepared) (Finding, error) {
	l := r.loop

	if p.Confirm != nil {
		r.writesUsed++
	}

	key := visitedKey(p.Capability, p.Args)
	if r.visited[key] {
		return Finding{}, fmt.Errorf("loop: %q selected but already visited — reachability filtering has a bug", key)
	}

	result, err := l.Exec.Dispatch(ctx, p.Capability, p.Args)
	if err != nil {
		return Finding{}, err
	}

	// "none" is a valid sentinel in the schema (§3) — a capability with
	// nothing worth reducing (kill_process's output is just confirmation,
	// not evidence) — but it was never registered as an actual reducer
	// name, so looking it up in the registry would fail. capabilities.
	// ValidateReducers already knows to skip it at load time; this is the
	// matching skip at run time.
	var reduced reduce.Result
	if p.Reducer == "none" {
		reduced = reduce.Result{Facts: map[string]any{"output": result.Output}}
	} else {
		reduced, err = l.Reduce.Reduce(p.Reducer, result.Output)
		if err != nil {
			return Finding{}, err
		}
	}

	finding := Finding{Step: r.step, Action: p.Capability, Facts: reduced.Facts, Empty: reduced.Empty}
	r.state.Findings = append(r.state.Findings, finding)
	for _, v := range reduced.Values {
		r.state.Values[v.Type] = append(r.state.Values[v.Type], ValueEntry{Value: v.Value, SourceStep: r.step})
	}
	r.visited[key] = true

	return finding, nil
}

// Decline records that a Prepared mutation's confirm was refused — stops
// the entire run (§4.3), never "skip this step and carry on."
func (r *Run) Decline(capability string) Termination {
	return Termination{Reason: ReasonDeclined, Detail: fmt.Sprintf("declined %s", capability)}
}

// ResolveAmbiguous supplies a human's pick for a Prepared step returned
// with Ambiguous set (§8.2) — built entirely from data already in hand,
// no second Judge call. Returns a fully-resolved Prepared: ready for
// Commit directly (safe capability) or carrying a Confirm request first
// (a mutation — resolving the target doesn't bypass §4.3).
func (r *Run) ResolveAmbiguous(prepared *Prepared, candidateID string) (*Prepared, error) {
	l := r.loop
	amb := prepared.Ambiguous
	if amb == nil {
		return nil, fmt.Errorf("loop: %q was not ambiguous", prepared.Capability)
	}

	var chosen *extract.Candidate
	for i := range amb.Candidates {
		if amb.Candidates[i].ID == candidateID {
			chosen = &amb.Candidates[i]
			break
		}
	}
	if chosen == nil {
		return nil, fmt.Errorf("loop: %q not among offered candidates", candidateID)
	}

	resolved, err := l.gateValue(r.state, amb.ArgType, *chosen)
	if err != nil {
		return nil, err
	}

	reg, ok := l.Capabilities.Get(prepared.Capability)
	if !ok {
		return nil, fmt.Errorf("loop: %q is not a known capability", prepared.Capability)
	}

	r.state.Resolved = append(r.state.Resolved, Resolved{
		Step: r.step, Capability: prepared.Capability, Arg: amb.ArgName,
		CandidateID: candidateID, Value: resolved, TargetResolvableNoul: amb.Noul,
	})

	out := &Prepared{
		Capability:       prepared.Capability,
		Args:             exec.Args{amb.ArgName: resolved},
		TargetDesc:       chosen.Desc,
		Reducer:          reg.Action.Reducer,
		Confidence:       prepared.Confidence,
		GoalAchievedNoul: prepared.GoalAchievedNoul,
	}
	if reg.Action.Danger != capabilities.DangerSafe {
		out.Confirm = &ConfirmRequest{
			Goal: r.state.Goal, Done: append([]Finding{}, r.state.Findings...),
			Capability: prepared.Capability, Danger: reg.Action.Danger, TargetDesc: chosen.Desc,
			WritesUsed: r.writesUsed, WriteBudget: l.Budgets.Writes,
			Step: r.step, StepBudget: l.Budgets.Steps,
		}
	}
	return out, nil
}

// Run drives a full run to completion synchronously, via l.Confirm — a
// convenience for callers that don't need to observe or pause between
// steps (the CLI). Built on NewRun/Prepare/Commit/Decline, the same
// primitives a step-driven caller (the TUI, §9) drives directly for its
// own async, message-driven control flow. Always returns a Termination —
// the loop must always exit with something, never a silent stop.
func (l *Loop) Run(ctx context.Context, goal string) (*State, Termination, error) {
	run := l.NewRun(goal)
	for {
		prepared, term, err := run.Prepare(ctx)
		if err != nil {
			return run.State(), Termination{}, err
		}
		if term != nil {
			return run.State(), *term, nil
		}

		if prepared.Ambiguous != nil {
			// No human to ask on this synchronous path (that's the TUI's
			// job via Run.ResolveAmbiguous) — same Termination shape the
			// old single-function step() produced, so this stays a
			// behavior-preserving refactor for every existing caller.
			amb := prepared.Ambiguous
			return run.State(), Termination{
				Reason: ReasonAmbiguousTarget,
				Detail: fmt.Sprintf("%s_target_resolvable noul %.2f, candidates: %v",
					amb.ArgType, amb.Noul, candidateDescs(amb.Candidates)),
			}, nil
		}

		if prepared.Confirm != nil {
			if l.Confirm == nil {
				return run.State(), Termination{}, fmt.Errorf(
					"loop: %q is a mutation but no Confirm callback is configured", prepared.Capability)
			}
			if !l.Confirm(*prepared.Confirm) {
				return run.State(), run.Decline(prepared.Capability), nil
			}
		}

		if _, err := run.Commit(ctx, prepared); err != nil {
			return run.State(), Termination{}, err
		}
	}
}

// resolveArg checks a target Choice against its paired target_resolvable
// Noul (§7). On a confident match it applies gate validation, records the
// decision in state.Resolved (§4.5) immediately, and returns the resolved
// value plus a human-facing description (for the confirm dialog, §4.3:
// "fully-resolved values, never the user's wording"). On an unconfident
// match — the picker-fallback case (§8.2) — it returns an AmbiguousChoice
// instead and records nothing yet; Run.ResolveAmbiguous records the
// decision once a human has picked.
func (l *Loop) resolveArg(
	state *State, capability string, arg capabilities.Arg, answers classify.Answers, candidates []extract.Candidate, step int,
) (value any, desc string, ambiguous *AmbiguousChoice, err error) {
	target := answers[string(arg.Type)+"_target"]
	resolvable := answers[string(arg.Type)+"_target_resolvable"]

	if resolvable.Noul < TargetResolvableFloor || target.Choice == "no_match" {
		return nil, "", &AmbiguousChoice{
			ArgType: arg.Type, ArgName: arg.Name, Candidates: candidates,
			Probabilities: target.Probabilities, Noul: resolvable.Noul,
		}, nil
	}

	for _, c := range candidates {
		if c.ID != target.Choice {
			continue
		}
		resolved, err := l.gateValue(state, arg.Type, c)
		if err != nil {
			return nil, "", nil, err
		}
		state.Resolved = append(state.Resolved, Resolved{
			Step: step, Capability: capability, Arg: arg.Name,
			CandidateID: target.Choice, Value: resolved, TargetResolvableNoul: resolvable.Noul,
		})
		return resolved, c.Desc, nil, nil
	}
	return nil, "", nil, fmt.Errorf("loop: %s_target chose %q, not among offered candidates", arg.Type, target.Choice)
}

// gateValue applies Layer 2 validation (§7) for one resolved candidate,
// dispatching by arg type. The pid path needs state (gate.ValidatePID
// checks against a real, current process listing); the path path doesn't.
func (l *Loop) gateValue(state *State, argType capabilities.ArgType, c extract.Candidate) (any, error) {
	switch argType {
	case capabilities.ArgPath:
		path, _ := c.Fields["path"].(string)
		resolved, err := gate.ValidatePath(path, l.PathRules)
		if err != nil {
			return nil, fmt.Errorf("gate rejected %q: %w", path, err)
		}
		return resolved, nil
	case capabilities.ArgPID:
		pid, _ := c.Fields["pid"].(int)
		if err := gate.ValidatePID(pid, knownProcesses(state), l.CurrentUser); err != nil {
			return nil, fmt.Errorf("gate rejected pid %d: %w", pid, err)
		}
		return pid, nil
	default:
		return nil, fmt.Errorf("loop: no gate for arg type %q", argType)
	}
}

// knownProcesses derives gate.ProcessInfo entries from state.Values["pid"]
// — populated only by unix__process_list's reducer, which is the only
// reducer that records an owner alongside a pid (reduce.ProcessLines'
// doc comment explains why). This is real, current process data, not a
// cache the loop maintains separately.
func knownProcesses(state *State) []gate.ProcessInfo {
	var out []gate.ProcessInfo
	for _, e := range state.Values["pid"] {
		m, ok := e.Value.(map[string]any)
		if !ok {
			continue
		}
		pid, ok := m["pid"].(int)
		if !ok {
			continue
		}
		owner, _ := m["owner"].(string)
		out = append(out, gate.ProcessInfo{PID: pid, Owner: owner})
	}
	return out
}

func candidateDescs(candidates []extract.Candidate) []string {
	out := make([]string, len(candidates))
	for i, c := range candidates {
		out[i] = c.Desc
	}
	return out
}

// stateValuesForExtraction flattens State.Values into the shape
// extract.Constructor expects, deduplicating identical values regardless
// of which step produced them — a Constructor asks "what's available,"
// not "what's the full history."
func stateValuesForExtraction(state *State) []reduce.Value {
	var out []reduce.Value
	seen := map[string]bool{}
	for typ, entries := range state.Values {
		for _, e := range entries {
			key := fmt.Sprintf("%s=%v", typ, e.Value)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, reduce.Value{Type: typ, Value: e.Value})
		}
	}
	return out
}

// stateForJudge is what actually goes over the wire as `state` (§6) — the
// goal and findings, matching phase 0's own validated state shape
// exactly (SPIKE.md's fixtures never carried a third "capabilities" key;
// the capability catalog only ever traveled via `questions.criteria`).
// Values/Resolved exist for extraction and oscillation detection,
// code-internal bookkeeping the model never needs to see directly.
//
// This used to also carry an unfiltered capability catalog for
// goal_satisfiable (questions.go) to reason over independently of
// next_action's narrower criteria. That's now built directly into
// goal_satisfiable's own instructions instead (goalSatisfiableInstructions)
// — phase 0's validated design keeps every question self-contained rather
// than relying on shared state a sibling question also reads, and a live
// side effect (a calibration-margin shift on an unrelated Noul, once
// state gained this second reader) is consistent with that dependency
// being worth avoiding.
func stateForJudge(state *State) map[string]any {
	return map[string]any{"goal": state.Goal, "findings": state.Findings}
}

// stateTokens is the same rough heuristic the phase-0 spike harness used
// (len(json)/4) — good enough to enforce §4.5's budget as a termination
// condition, not a precise token count.
func stateTokens(state *State) int {
	b, err := json.Marshal(state)
	if err != nil {
		return 0
	}
	return len(b) / 4
}
