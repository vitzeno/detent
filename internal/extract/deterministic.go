package extract

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/vitzeno/detent/internal/capabilities"
)

// DeterministicConstructor is the default Constructor (§5.1) — the same
// fuzzy path matching and state-lookup table from §5, no model involved.
// An LLM-assisted Constructor (§5.1's "assisted" mode, §11) is a future
// implementation behind this same interface; nothing about that mode
// changes anything here.
type DeterministicConstructor struct {
	// Root is the directory fuzzy path matching walks when state supplies
	// no path values yet. Defaults to "." when empty.
	Root string
}

func (c *DeterministicConstructor) Candidates(_ context.Context, argType capabilities.ArgType, goalText string, state State) ([]Candidate, error) {
	switch argType {
	case capabilities.ArgPath:
		return c.pathCandidates(goalText, state)
	case capabilities.ArgPID:
		return pidCandidates(goalText, state), nil
	default:
		return nil, fmt.Errorf("extract: no deterministic extraction implemented for arg type %q", argType)
	}
}

func (c *DeterministicConstructor) pathCandidates(goalText string, state State) ([]Candidate, error) {
	if fromState := pathCandidatesFromState(state); len(fromState) > 0 {
		return capAt(fromState, MaxCandidates), nil
	}
	return c.pathCandidatesFromGoalText(goalText)
}

// pathCandidatesFromState honors §5's ordering rule: "facts already in
// state are the first source of candidates — a pid the loop already
// discovered outranks anything extracted from the original sentence."
// Once the real loop (step 7) accumulates state.Values across iterations,
// this branch is what lets a later step resolve against what an earlier
// step found, without ever re-deriving it from the goal's wording.
func pathCandidatesFromState(state State) []Candidate {
	var out []Candidate
	for _, v := range state.Values {
		if v.Type != string(capabilities.ArgPath) {
			continue
		}
		p, ok := v.Value.(string)
		if !ok || p == "" {
			continue
		}
		out = append(out, Candidate{
			ID:     pathCandidateID(p),
			Desc:   p,
			Fields: map[string]any{"path": p},
		})
	}
	return out
}

// pathCandidatesFromGoalText is the fallback (§5: "fuzzy-match goal text
// against cwd tree → shortlist") for when nothing in state has surfaced a
// path yet — iteration one, before any read has happened.
func (c *DeterministicConstructor) pathCandidatesFromGoalText(goalText string) ([]Candidate, error) {
	root := c.Root
	if root == "" {
		root = "."
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("extract: reading %s: %w", root, err)
	}

	tokens := goalTokens(goalText)
	type scored struct {
		name  string
		score int
	}
	var matches []scored
	for _, e := range entries {
		if s := tokenOverlapScore(e.Name(), tokens); s > 0 {
			matches = append(matches, scored{name: e.Name(), score: s})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })

	candidates := make([]Candidate, 0, len(matches))
	for _, m := range matches {
		p := filepath.Join(root, m.name)
		candidates = append(candidates, Candidate{
			ID:     pathCandidateID(p),
			Desc:   p,
			Fields: map[string]any{"path": p},
		})
	}
	return capAt(candidates, MaxCandidates), nil
}

// goalTokens splits goal text into lowercase word tokens — deliberately
// simple regex/heuristic extraction (§5: "regex/heuristic from the goal
// text; often no model involvement").
func goalTokens(goalText string) []string {
	return strings.FieldsFunc(strings.ToLower(goalText), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// tokenOverlapScore counts goal tokens that appear as a substring of name.
// Tokens shorter than 3 characters are skipped — short words ("a", "in",
// "go") match too much of a typical directory listing to mean anything.
func tokenOverlapScore(name string, tokens []string) int {
	lower := strings.ToLower(name)
	score := 0
	for _, tok := range tokens {
		if len(tok) < 3 {
			continue
		}
		if strings.Contains(lower, tok) {
			score++
		}
	}
	return score
}

// pidCandidates mirrors pathCandidates' ordering rule (state first) but
// its goal-text fallback is a numeric literal, not a fuzzy filename match
// (§5: "pid: facts already in state, the fetched process list, or a
// numeric literal") — there's no filesystem to walk for a pid.
//
// A literal-derived candidate carries no owner: it was never checked
// against a real process list, so gate.ValidatePID (§7) will correctly
// refuse it unless a real unix__process_list run has *separately* put a
// matching, owned pid into state — a literal alone is never enough to
// authorize a kill, only enough to suggest a candidate.
func pidCandidates(goalText string, state State) []Candidate {
	if fromState := pidCandidatesFromState(goalText, state); len(fromState) > 0 {
		return capAt(fromState, MaxCandidates)
	}
	return capAt(pidCandidatesFromGoalText(goalText), MaxCandidates)
}

// pidCandidatesFromState mirrors pathCandidatesFromGoalText's relevance
// filtering, applied here instead of at reduction time (reduce.ProcessLines
// has no goal text to filter by — only extraction does). Found necessary
// running this live: a real process list can carry 50+ entries even after
// reduce's own cap, comfortably over Choice's 255-option ceiling if ever
// offered unfiltered, and useless as a target Choice regardless — a wall
// of every running process doesn't help the model find the one meant.
//
// When the goal's wording matches at least one process' cmd, only matches
// are offered. When nothing matches — a positional/superlative reference
// like "the one using the most memory" — everything survives, capped:
// this reducer only captures pid/owner/cmd, not %cpu/%mem, so ranking by
// resource use isn't possible yet (a real future extension; phase 0b's
// spike validated the superlative category works well once that data
// exists — SPIKE_TARGET_RESOLUTION.md — it just isn't wired to a real
// capability today).
func pidCandidatesFromState(goalText string, state State) []Candidate {
	tokens := goalTokens(goalText)
	var all, matched []Candidate
	for _, v := range state.Values {
		if v.Type != string(capabilities.ArgPID) {
			continue
		}
		m, ok := v.Value.(map[string]any)
		if !ok {
			continue
		}
		pid, ok := m["pid"].(int)
		if !ok {
			continue
		}
		owner, _ := m["owner"].(string)
		cmd, _ := m["cmd"].(string)
		cand := Candidate{
			ID:     pidCandidateID(pid),
			Desc:   fmt.Sprintf("pid %d, %s, owned by %s", pid, cmd, owner),
			Fields: map[string]any{"pid": pid, "owner": owner, "cmd": cmd},
		}
		all = append(all, cand)
		if tokenOverlapScore(cmd, tokens) > 0 {
			matched = append(matched, cand)
		}
	}
	if len(matched) > 0 {
		return matched
	}
	return all
}

var pidLiteralPattern = regexp.MustCompile(`\b\d+\b`)

func pidCandidatesFromGoalText(goalText string) []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	for _, m := range pidLiteralPattern.FindAllString(goalText, -1) {
		if seen[m] {
			continue
		}
		seen[m] = true
		pid, err := strconv.Atoi(m)
		if err != nil {
			continue
		}
		out = append(out, Candidate{
			ID:     pidCandidateID(pid),
			Desc:   fmt.Sprintf("pid %d (from the goal text — not yet verified against a real process)", pid),
			Fields: map[string]any{"pid": pid},
		})
	}
	return out
}

func pidCandidateID(pid int) string {
	return fmt.Sprintf("pid_%d", pid)
}

func pathCandidateID(path string) string {
	id := strings.NewReplacer("/", "_", ".", "_", " ", "_").Replace(path)
	if id == "" {
		id = "root"
	}
	return "path_" + id
}

func capAt(candidates []Candidate, max int) []Candidate {
	if len(candidates) > max {
		return candidates[:max]
	}
	return candidates
}
