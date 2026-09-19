package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/usage"
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
	// JudgeUsage is what the batch consumed; zero on fallback.
	JudgeUsage usage.Usage
}

// NewPreJudgment returns a PreJudgment with ScopeRisk defaulted to -1
// ("unknown" — a real score is always in [0,1]). Both judgePre's
// fallback and its Jev-answered path start from this same shape, so a
// future call site can't silently default ScopeRisk to 0.0 — a
// valid-looking "no risk" score — instead of "unknown".
func NewPreJudgment(dangerous bool, note string) PreJudgment {
	return PreJudgment{ScopeRisk: -1, Dangerous: dangerous, RiskNote: note}
}

// NewPostJudgment returns a PostJudgment with Attention/GoalAchieved
// defaulted to -1 ("unknown"), for the same reason as NewPreJudgment.
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
		// Structured {what, not_for, examples} criteria, not flat strings:
		// with nine options this Choice is exactly where option count
		// makes confidence calibration suffer without it — the same
		// fix this project already validated for next_action/goal_satisfiable.
		"render_kind": {
			Instructions: "What shape is this command's output? Pick how a human should read it.",
			Choice: &classify.ChoiceQuestion{Criteria: map[string]any{
				KindInline: map[string]any{
					"what":     "a few short lines that fit inline under the command — naturally brief, not just truncated",
					"not_for":  "a long-running command that happened to produce little output — that's KindQuiet, not this",
					"examples": []string{"pwd", "echo done", "git rev-parse HEAD"},
				},
				KindQuiet: map[string]any{
					"what":     "a long-running or build-like command that produced almost no output — a one-line done suffices",
					"not_for":  "a command that is always naturally short — that's KindInline even if it also ran quickly",
					"examples": []string{"npm install, finished with a minimal log", "a background service start with no output"},
				},
				KindLog: map[string]any{
					"what":     "a long log, listing, or build/test output — many lines, read top to bottom for events over time",
					"not_for":  "a file's own prose or code body (KindContent), or output whose main point is one failure (KindError)",
					"examples": []string{"go test ./... output", "a docker build log", "tail -n 200 app.log"},
				},
				KindTable: map[string]any{
					"what":     "aligned columns with a header row — reads as a table, one record per row",
					"not_for":  "a bare list of paths or names with no header or columns — that's KindFiles",
					"examples": []string{"ps aux", "df -h", "ls -la"},
				},
				KindFiles: map[string]any{
					"what":     "a list of paths or items to pick from, one per line, with no header or aligned columns",
					"not_for":  "the same kind of listing but with a header row and aligned columns — that's KindTable",
					"examples": []string{"find . -name '*.go'", "git diff --name-only", "plain ls, one entry per line"},
				},
				KindContent: map[string]any{
					"what":     "a file's own prose or code body, read in full like a document — reads best with line numbers",
					"not_for":  "well-formed JSON even if it came from cat — that's KindJSON; or scanning output for events — that's KindLog",
					"examples": []string{"cat main.go", "cat README.md"},
				},
				KindError: map[string]any{
					"what":     "an error, traceback, or compiler complaint that is the command's main point — reads in full, in order",
					"not_for":  "a log that merely contains some warnings among mostly normal output — that's still KindLog",
					"examples": []string{"a failed build's compiler error", "a stack trace", "command not found"},
				},
				KindDiff: map[string]any{
					"what":     "a unified diff — +/- lines with @@ hunk headers — reads with added/removed coloring",
					"not_for":  "output that merely describes changes in prose — must be the literal unified diff format",
					"examples": []string{"git diff", "diff -u a.txt b.txt"},
				},
				KindJSON: map[string]any{
					"what":     "JSON or other structured data, meant to be read as data — reads pretty-printed",
					"not_for":  "a file's prose or code body that merely happens not to be JSON",
					"examples": []string{"curl ... returning a JSON body", "kubectl get pod -o json"},
				},
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
	fb := NewPreJudgment(dangerous, note)
	answers, ju, ok := classify.AskOrFallback(ctx, s.Judge,
		classify.State(map[string]any{"goal": goal, "command": command}),
		preQuestions())
	if !ok {
		return fb
	}
	out := NewPreJudgment(dangerous, note)
	out.FromJudge = true
	out.JudgeUsage = convertUsage(ju)
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
