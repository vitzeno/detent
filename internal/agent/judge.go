package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/usage"
	"github.com/vitzeno/detent/internal/viewgen"
)

// Mut* tiers; "" means unknown, renders neutral never safe.
const (
	MutReadOnly     = "read_only"
	MutWorkspace    = "writes_workspace"
	MutSystem       = "system_affecting"
	MutIrreversible = "likely_irreversible"
)

// RunMode* values; "" means no RunnerSelector was configured.
const (
	RunModeHost    = "host"
	RunModeSandbox = "sandbox"
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
	// RunMode is which Runner Select picked for this command; "" if
	// no RunnerSelector was wired.
	RunMode string
	// JudgeUsage is what the batch consumed; zero on fallback.
	JudgeUsage usage.Usage
}

// Status* values; "" means unknown.
const (
	StatusClean    = "clean_success"
	StatusWarnings = "success_with_warnings"
	StatusFailed   = "failed"
	StatusEmpty    = "empty"
)

// Kind* values come from viewgen, which is the only thing that
// consumes them: they choose a view, prune what a generated one may
// use, and key the spec saved for it. "" still means unknown.
const (
	KindText    = viewgen.KindText
	KindTable   = viewgen.KindTable
	KindFiles   = viewgen.KindFiles
	KindContent = viewgen.KindContent
	KindError   = viewgen.KindError
	KindDiff    = viewgen.KindDiff
	KindJSON    = viewgen.KindJSON
)

// PostJudgment classifies a finished command for rendering.
type PostJudgment struct {
	FromJudge        bool
	Status           string
	StatusConfidence float64
	RenderKind       string
	Attention        float64
	GoalAchieved     float64
	// JudgeUsage is what the batch consumed; zero on fallback.
	JudgeUsage usage.Usage
}

// NewPreJudgment defaults ScopeRisk to -1 ("unknown"; a real score is
// always in [0,1]) rather than a valid-looking 0.0.
func NewPreJudgment(dangerous bool, note string) PreJudgment {
	return PreJudgment{ScopeRisk: -1, Dangerous: dangerous, RiskNote: note}
}

// NewPostJudgment defaults Attention/GoalAchieved to -1 ("unknown").
func NewPostJudgment() PostJudgment {
	return PostJudgment{Attention: -1, GoalAchieved: -1}
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
			Choice:       &classify.ChoiceQuestion{Criteria: viewgen.RenderKindCriteria()},
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
	out := NewPreJudgment(dangerous, note)
	answers, ju, ok := classify.AskOrFallback(ctx, s.Judge,
		classify.State(map[string]any{"goal": goal, "command": command}),
		preQuestions())
	if ok {
		out.FromJudge = true
		out.JudgeUsage = convertUsage(ju)
		if a, aok := answers["mutability"]; aok && a.Choice != "" {
			out.Mutability = a.Choice
			out.MutabilityConfidence = a.Confidence
			if a.Choice == MutSystem || a.Choice == MutIrreversible {
				out.Dangerous = true
				out.RiskNote = joinNote(out.RiskNote, "jev mutability: "+a.Choice)
			}
		}
		if a, aok := answers["scope_risk"]; aok {
			out.ScopeRisk = a.Noul
			if a.Noul >= s.riskThreshold() {
				out.Dangerous = true
				out.RiskNote = joinNote(out.RiskNote, fmt.Sprintf("jev scope risk %.2f", a.Noul))
			}
		}
	}
	if s.Runners != nil {
		_, out.RunMode = s.Runners.Select(out)
	}
	return out
}

func (s *Session) judgePost(ctx context.Context, goal, command string, res resultView) PostJudgment {
	fb := heuristicPost(res)
	answers, ju, ok := classify.AskOrFallback(ctx, s.Judge,
		classify.State(map[string]any{
			"goal":      goal,
			"command":   command,
			"exit_code": res.ExitCode,
			"output":    res.Output,
		}),
		postQuestions())
	if !ok {
		return fb
	}
	out := NewPostJudgment()
	out.FromJudge = true
	out.JudgeUsage = convertUsage(ju)
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

// convertUsage translates a Judge report; LatencyMS is always wall time
// measured by AskOrFallback, not the adapter's own clock.
func convertUsage(u classify.Usage) usage.Usage {
	return usage.Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		Latency:          time.Duration(u.LatencyMS * float64(time.Millisecond)),
		Model:            u.Model,
	}
}

type resultView struct {
	ExitCode int
	Output   string // stdout+stderr, bounded
	Lines    int
}

func heuristicPost(res resultView) PostJudgment {
	out := NewPostJudgment()
	switch {
	case res.ExitCode != 0:
		out.Status = StatusFailed
		out.RenderKind = KindError
		out.Attention = 0.9
	case res.Lines == 0:
		out.Status = StatusEmpty
		out.RenderKind = KindText
		out.Attention = 0.1
	default:
		out.Status = StatusClean
		out.Attention = 0.1
		// No length branch: the viewport scrolls whatever it is given,
		// and viewgen gates a model call on size itself.
		out.RenderKind = KindText
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
