package classify

import (
	"context"
	"fmt"

	"github.com/vitzeno/detent/event"
)

// DefaultRiskThreshold is the scope risk at which a command is flagged.
const DefaultRiskThreshold = 0.5

// RiskJudge adapts Jev to the engine's hook chain. It answers, it
// never decides: Widen folds it with everyone else's.
type RiskJudge struct {
	Asker     Asker
	Threshold float64
}

// Assess asks the two pre-execution questions. An unreachable judge is
// no answer, not an error the Turn should end on.
func (j RiskJudge) Assess(ctx context.Context, command string, threshold float64) (event.Risk, error) {
	if threshold <= 0 {
		threshold = j.Threshold
	}
	if threshold <= 0 {
		threshold = DefaultRiskThreshold
	}
	answers, _, ok := AskOrFallback(ctx, j.Asker, State(map[string]any{"command": command}), riskQuestions())
	if !ok {
		return event.Risk{}, nil
	}
	out := event.Risk{FromJudge: true, ScopeRisk: -1}
	if a, has := answers["mutability"]; has && a.Choice != "" {
		out.Mutability = a.Choice
		if a.Choice == event.MutSystem || a.Choice == event.MutIrreversible {
			out.Dangerous = true
			out.Note = "jev: " + a.Choice
		}
	}
	if a, has := answers["scope_risk"]; has {
		out.ScopeRisk = a.Noul
		if a.Noul >= threshold {
			out.Dangerous = true
			out.Note = joinRiskNote(out.Note, fmt.Sprintf("jev scope risk %.2f", a.Noul))
		}
	}
	return out, nil
}

func riskQuestions() Questions {
	return Questions{
		"mutability": {
			Instructions: "Classify what this shell command could change if it runs successfully. " +
				"Judge the command text alone.",
			Choice: &ChoiceQuestion{Criteria: map[string]any{
				event.MutRead:         "reads state only: listing, viewing, searching, checking status — nothing is created, modified, or deleted",
				event.MutWorkspace:    "writes inside the working area: creates, modifies, moves, or deletes files the request is about — no system-wide effect",
				event.MutSystem:       "affects the system beyond the working area: installs, restarts services, changes permissions, touches other users' state",
				event.MutIrreversible: "likely irreversible: mass deletion, disk writes, forced pushes, dropping data, shutting down",
			}},
		},
		"scope_risk": {
			Instructions: "Would running this shell command plausibly affect an unexpectedly " +
				"large scope — many files, data loss, other hosts, production state?",
			Noul: &NoulQuestion{},
		},
	}
}

func joinRiskNote(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}
