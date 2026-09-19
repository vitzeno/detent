package loop

import (
	"fmt"
	"strings"

	"github.com/vitzeno/detent/internal/capabilities"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/exec"
	"github.com/vitzeno/detent/internal/extract"
)

// doneCriterion and cannotProceedCriterion are ported verbatim from phase
// 0's validated TERMINALS dict (experiments/scripts/spike.py) — four
// rounds of wording iteration, including cannot_proceed's structured
// {what, not_for, examples} form, the single most-iterated piece of
// criteria text in the whole spike.
var doneCriterion any = "The goal has been achieved; nothing further is needed"

var cannotProceedCriterion any = map[string]any{
	"what": "No capability in this list can perform the specific action the goal names, " +
		"even if one of them touches the same general area (files, processes, git, disk, " +
		"logs). Before picking any other option, check the goal's actual verb (rename, " +
		"restart, deploy, install, update, upgrade, encrypt, send, compress, translate, " +
		"etc.) against what each capability literally does — not just whether the goal " +
		"mentions a matching noun like 'file' or 'process'. Investigating, reading, or " +
		"listing information related to the goal is not the same as performing it — " +
		"inspecting what exists does not install, update, upgrade, deploy, rename, or " +
		"otherwise change anything. Choose this when running every capability here would " +
		"still leave the goal's actual action undone.",
	"not_for": "A goal that some capability here can directly satisfy, including as a " +
		"first investigative step before a further action that this same list can also " +
		"perform.",
	"examples": []string{
		"compress this video", "translate this document to French",
		"reboot the router", "print this page", "encrypt this file",
		"upgrade the database schema",
	},
}

// actionCriterion mirrors phase 0's CAPABILITIES dict shape: a plain
// string when there's no confusable-sibling note worth making, a
// structured {what, not_for} object when there is (Action.NotFor set).
func actionCriterion(a capabilities.Action) any {
	if a.NotFor == "" {
		return a.Description
	}
	return map[string]any{"what": a.Description, "not_for": a.NotFor}
}

// reachableCriteria builds next_action's Choice criteria (§4.2): every
// zero-required-arg capability not yet visited, plus a
// single-required-arg capability (read_file's path, kill_process's pid)
// when at least one of its candidates hasn't already been visited under
// that specific capability. A repeat is filtered out here, at the source
// — never offered at all — rather than rejected after Jev picks it (§4.1:
// "a repeat is not selectable").
//
// It also returns the candidates actually offered, keyed by arg type —
// filtered against whichever capability ended up reachable for that type,
// so callers never need to hardcode a capability name to keep a target
// Choice's own criteria consistent with what was offered here.
//
// Multi-arg capabilities (more than one required arg) aren't made
// reachable here — no capability in the catalog needs that shape yet;
// real future work, not a silent gap.
func reachableCriteria(
	capReg *capabilities.Registry,
	visited map[string]bool,
	candidatesByType map[capabilities.ArgType][]extract.Candidate,
) (map[string]any, map[capabilities.ArgType][]extract.Candidate) {
	criteria := map[string]any{
		"done":           doneCriterion,
		"cannot_proceed": cannotProceedCriterion,
	}
	offered := map[capabilities.ArgType][]extract.Candidate{}

	for _, reg := range capReg.All() {
		required := requiredArgs(reg.Action)
		switch {
		case len(required) == 0:
			// Zero *required* args — list_files' optional path arg still
			// lands here, dispatched with defaults, exactly like a
			// truly argless action.
			if !visited[visitedKey(reg.QualifiedName, exec.Args{})] {
				criteria[reg.QualifiedName] = actionCriterion(reg.Action)
			}
		case len(required) == 1:
			arg := required[0]
			var unvisited []extract.Candidate
			for _, c := range candidatesByType[arg.Type] {
				key := visitedKey(reg.QualifiedName, exec.Args{arg.Name: c.Fields[arg.Name]})
				if !visited[key] {
					unvisited = append(unvisited, c)
				}
			}
			if len(unvisited) > 0 {
				criteria[reg.QualifiedName] = actionCriterion(reg.Action)
				offered[arg.Type] = unvisited
			}
		}
	}
	return criteria, offered
}

// requiredArgs filters an action's args down to the required ones — an
// optional arg (list_files' path) never blocks reachability the way a
// required one does.
func requiredArgs(a capabilities.Action) []capabilities.Arg {
	var out []capabilities.Arg
	for _, arg := range a.Args {
		if arg.Required {
			out = append(out, arg)
		}
	}
	return out
}

// goalSatisfiableInstructions builds a self-contained instructions string
// for goal_satisfiable — a terse capability gist list baked directly into
// the text, deliberately not a reference to shared state or another
// question's criteria. This mirrors phase 0's own validated design
// exactly (spike.py's comment on the pattern): "self-contained ... to
// stay cheap and to work whether or not sibling question definitions are
// visible to each other." Pointing this at next_action's own (iteration-
// narrowed) criteria instead — tried live while building the TUI —
// produced a real bug (a two-step goal scored unsatisfiable on iteration
// one, before its second step had a chance to become reachable) and,
// separately, a shift in this Noul's own calibration margin on clearly
// unsatisfiable goals. Building the list from the domain's full,
// unfiltered catalog here avoids both: it can't be narrowed by what
// happens to be reachable this iteration, and it doesn't depend on
// whether the model can see across sibling questions in the same call.
func goalSatisfiableInstructions(capReg *capabilities.Registry) string {
	var gists []string
	for _, reg := range capReg.All() {
		gists = append(gists, reg.Action.Description)
	}
	return "The only actions available are: " + strings.Join(gists, "; ") + ". Can the goal be " +
		"fully accomplished by using one or more of these in sequence — for example, using one " +
		"to identify a target and a second to act on it? Answer yes if some combination or " +
		"single use of them reaches the goal. Answer no only if the goal needs a different " +
		"action entirely — installing, updating, upgrading, deploying, renaming, restarting a " +
		"service, sending a message, or anything else not in that list — even if the goal " +
		"mentions a file or process that these can inspect. Investigating or reading about " +
		"something is not the same as doing it."
}

// buildQuestions assembles one iteration's full batched call (§4, §4.1,
// §4.2, §6.1): next_action, the goal_achieved termination test,
// goal_satisfiable, and — one pair per arg type that has something to
// disambiguate — a speculative `<type>_target` / `<type>_target_resolvable`
// pair (§6.1's own naming: path_target, pid_target).
func buildQuestions(
	capReg *capabilities.Registry,
	criteria map[string]any,
	offered map[capabilities.ArgType][]extract.Candidate,
) classify.Questions {
	qs := classify.Questions{
		"next_action": {
			// Phase 0's validated instructions are this terse — the
			// disambiguation work lives in the criteria text (above), not
			// instruction prose. One addition beyond phase 0: phase 0's
			// next_action always saw its full static 12-option catalog
			// every call, so it never needed to know an option could be
			// missing. This implementation's reachability narrowing
			// (§4.2) is a mechanism phase 0 never had, so it gets its own
			// short note rather than assuming phase 0's wording covers it.
			Instructions: "Given the goal and the findings so far, what should happen next? " +
				"Note: a capability the goal will eventually need (e.g. reading a specific file) " +
				"may not be listed yet if its target hasn't been discovered — that's expected, " +
				"not a sign nothing here is right. Confidently pick whichever listed capability " +
				"makes real progress toward discovering it.",
			Choice: &classify.ChoiceQuestion{Criteria: criteria},
		},
		"goal_achieved": {
			// Phase 0's validated instructions, plus one addition: phase
			// 0's fixtures stood in for reducers with hand-authored
			// "reduced" facts, which never specifically exercised a goal
			// phrased around the very content reduction removes ("show me
			// the *contents* of X"). Found live: without this note,
			// next_action's confidence in "done" after a genuinely
			// successful read_file landed ~0.6, under the 0.8 floor,
			// because the model correctly noticed the reduced preview
			// isn't itself "the contents." Raised to ~0.9, stable across
			// repeated runs, once told what a preview represents.
			Instructions: "Has the stated goal been fully achieved given the findings so far? " +
				"A finding's fields (e.g. a text preview, a byte/line count) are evidence the " +
				"underlying action already completed successfully — a short reduction for this " +
				"decision, not the full result. A read_file finding with a preview means the " +
				"file's full contents were already retrieved and will be shown to the user in " +
				"full elsewhere, even though only a short preview appears here.",
			Noul: &classify.NoulQuestion{},
		},
		"goal_satisfiable": {
			Instructions: goalSatisfiableInstructions(capReg),
			Noul:         &classify.NoulQuestion{},
		},
	}

	for argType, candidates := range offered {
		if len(candidates) == 0 {
			continue
		}
		addTargetQuestions(qs, string(argType), candidates)
	}
	return qs
}

// addTargetQuestions adds the speculative `<argType>_target` Choice and
// its paired `<argType>_target_resolvable` Noul (§4.2, §6.1) — asked in
// the same call as next_action regardless of whether next_action ends up
// picking something that needs this arg; a few extra input tokens,
// discarded when irrelevant.
func addTargetQuestions(qs classify.Questions, argType string, candidates []extract.Candidate) {
	criteria := make(map[string]any, len(candidates)+1)
	for _, c := range candidates {
		criteria[c.ID] = c.Desc
	}
	criteria["no_match"] = fmt.Sprintf("None of these %s candidates match what the goal refers to.", argType)

	qs[argType+"_target"] = classify.Question{
		Instructions: fmt.Sprintf("If a %s is needed for the next action, which of these "+
			"candidates does the goal refer to? Choose no_match if none match or more than one "+
			"plausibly does.", argType),
		Choice: &classify.ChoiceQuestion{Criteria: criteria},
	}
	qs[argType+"_target_resolvable"] = classify.Question{
		Instructions: fmt.Sprintf("Does exactly one %s candidate unambiguously match what the "+
			"goal refers to?", argType),
		Noul: &classify.NoulQuestion{},
	}
}
