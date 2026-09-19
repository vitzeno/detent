package agentloop

import (
	"context"
	"fmt"
	"strings"

	"github.com/vitzeno/detent/internal/classify"
)

// Mut* tiers; "" means unknown, renders neutral never safe.
const (
	MutReadOnly     = "read_only"
	MutWorkspace    = "writes_workspace"
	MutSystem       = "system_affecting"
	MutIrreversible = "likely_irreversible"
)

// PreJudgment classifies a proposed command before confirm.
type PreJudgment struct {
	FromJudge            bool
	Mutability           string
	MutabilityConfidence float64
	ScopeRisk            float64
	// Dangerous/RiskNote is escalate-only: FlagDanger or scope risk above threshold.
	Dangerous bool
	RiskNote  string
}

// Status* values; "" means unknown.
const (
	StatusClean    = "clean_success"
	StatusWarnings = "success_with_warnings"
	StatusFailed   = "failed"
	StatusEmpty    = "empty"
)

// Kind* values; "" means unknown.
const (
	KindInline  = "inline_short"
	KindLog     = "scrollable_log"
	KindTable   = "table"
	KindFiles   = "file_listing"
	KindContent = "file_content"
	KindError   = "error_text"
	KindDiff    = "diff"
	KindJSON    = "structured_json"
	KindQuiet   = "quiet_progress"
)

// PostJudgment classifies a finished command for rendering.
type PostJudgment struct {
	FromJudge        bool
	Status           string
	StatusConfidence float64
	RenderKind       string
	Attention        float64
	GoalAchieved     float64
}

func preQuestions() classify.Questions {
	return classify.Questions{
		"mutability": {
			Instructions: "Classify what this shell command could change if it runs successfully. " +
				"Judge the command text alone — not the goal, not prior output.",
			Choice: &classify.ChoiceQuestion{Criteria: map[string]any{
				MutReadOnly:     "reads state only: listing, viewing, searching, checking status — nothing is created, modified, or deleted",
				MutWorkspace:    "writes inside the working area: creates, modifies, moves, or deletes files the goal is about — no system-wide effect",
				MutSystem:       "affects the system beyond the working area: installs, restarts services, changes permissions, touches other users' state",
				MutIrreversible: "likely irreversible: mass deletion, disk writes, forced pushes, dropping data, shutting down",
			}},
		},
		"scope_risk": {
			Instructions: "Would running this shell command plausibly affect an unexpectedly " +
				"large scope — many files, data loss, other hosts, production state — " +
				"beyond what the stated goal asks for?",
			Noul: &classify.NoulQuestion{},
		},
	}
}

func postQuestions() classify.Questions {
	return classify.Questions{
		"result_status": {
			Instructions: "Given the command and its captured output, how did it go? " +
				"Warnings means it succeeded but the output contains errors, retries, or deprecations worth noticing.",
			Choice: &classify.ChoiceQuestion{Criteria: map[string]any{
				StatusClean:    "succeeded with clean, expected output",
				StatusWarnings: "succeeded but output contains warnings, errors, or retries worth noticing",
				StatusFailed:   "failed: non-zero exit or output showing the command did not do its job",
				StatusEmpty:    "ran fine but produced no useful output",
			}},
		},
		"render_kind": {
			Instructions: "What shape is this command's output? Pick how a human should read it.",
			Choice: &classify.ChoiceQuestion{Criteria: map[string]any{
				KindInline:  "a few short lines — fits inline under the command",
				KindLog:     "a long log, listing, or build output — needs a scrollable viewport",
				KindTable:   "aligned columns with a header row (ps, df, ls -la) — reads as a table",
				KindFiles:   "a list of paths or items to pick from — reads best one per line",
				KindContent: "a file's prose or code body — reads with line numbers",
				KindError:   "an error, traceback, or compiler complaint — reads in full, in order",
				KindDiff:    "a unified diff (+/- lines) — reads with added/removed coloring",
				KindJSON:    "JSON or other structured data — reads pretty-printed",
				KindQuiet:   "barely any output from a long-running command — a one-line done suffices",
			}},
		},
		"attention": {
			Instructions: "Does this command's outcome need the human's attention before " +
				"continuing — a failure, a surprise, a destructive result — rather than " +
				"being safe to collapse to one line?",
			Noul: &classify.NoulQuestion{},
		},
		"goal_achieved": {
			Instructions: "Has the stated goal been achieved given everything observed so far? " +
				"This is a second opinion — another model proposes the commands; judge only " +
				"whether the goal itself looks satisfied.",
			Noul: &classify.NoulQuestion{},
		},
	}
}

func (s *Session) judgePre(ctx context.Context, goal, command string) PreJudgment {
	dangerous, note := FlagDanger(command)
	fb := PreJudgment{ScopeRisk: -1, Dangerous: dangerous, RiskNote: note}
	if s.Judge == nil {
		return fb
	}
	answers, _, err := s.Judge.Ask(ctx,
		classify.State(map[string]any{"goal": goal, "command": command}),
		preQuestions())
	if err != nil {
		return fb
	}
	out := PreJudgment{FromJudge: true, ScopeRisk: -1, Dangerous: dangerous, RiskNote: note}
	if a, ok := answers["mutability"]; ok && a.Choice != "" {
		out.Mutability = a.Choice
		out.MutabilityConfidence = a.Confidence
		if a.Choice == MutSystem || a.Choice == MutIrreversible {
			out.Dangerous = true
			out.RiskNote = joinNote(out.RiskNote, "jev mutability: "+a.Choice)
		}
	}
	if a, ok := answers["scope_risk"]; ok {
		out.ScopeRisk = a.Noul
		if a.Noul >= s.riskThreshold() {
			out.Dangerous = true
			out.RiskNote = joinNote(out.RiskNote, fmt.Sprintf("jev scope risk %.2f", a.Noul))
		}
	}
	return out
}

func (s *Session) judgePost(ctx context.Context, goal, command string, res resultView) PostJudgment {
	fb := heuristicPost(res)
	if s.Judge == nil {
		return fb
	}
	answers, _, err := s.Judge.Ask(ctx,
		classify.State(map[string]any{
			"goal":      goal,
			"command":   command,
			"exit_code": res.ExitCode,
			"output":    res.Output,
		}),
		postQuestions())
	if err != nil {
		return fb
	}
	out := PostJudgment{FromJudge: true, Attention: -1, GoalAchieved: -1}
	if a, ok := answers["result_status"]; ok && a.Choice != "" {
		out.Status = a.Choice
		out.StatusConfidence = a.Confidence
	} else {
		out.Status = fb.Status
	}
	if a, ok := answers["render_kind"]; ok && a.Choice != "" {
		out.RenderKind = a.Choice
	} else {
		out.RenderKind = fb.RenderKind
	}
	if a, ok := answers["attention"]; ok {
		out.Attention = a.Noul
	}
	if a, ok := answers["goal_achieved"]; ok {
		out.GoalAchieved = a.Noul
	}
	return out
}

type resultView struct {
	ExitCode int
	Output   string // stdout+stderr, bounded
	Lines    int
}

func heuristicPost(res resultView) PostJudgment {
	out := PostJudgment{Attention: -1, GoalAchieved: -1}
	switch {
	case res.ExitCode != 0:
		out.Status = StatusFailed
		out.RenderKind = KindError
		out.Attention = 0.9
	case res.Lines == 0:
		out.Status = StatusEmpty
		out.RenderKind = KindInline
		out.Attention = 0.1
	default:
		out.Status = StatusClean
		out.Attention = 0.1
		if res.Lines <= 10 {
			out.RenderKind = KindInline
		} else {
			out.RenderKind = KindLog
		}
	}
	return out
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
