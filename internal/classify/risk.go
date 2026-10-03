package classify

import (
	"context"
	"fmt"
	"time"

	"github.com/vitzeno/detent/event"
)

const (
	// DefaultRiskThreshold is the scope risk at which a command is flagged.
	DefaultRiskThreshold = 0.5
	// DefaultRiskTimeout bounds one assessment. Every tool call waits on it,
	// and an answer usually takes well under a second.
	DefaultRiskTimeout = 5 * time.Second
)

// RiskJudge adapts Jev to the engine's hook chain. It answers, it
// never decides: Widen folds it with everyone else's.
type RiskJudge struct {
	Asker     Asker
	Threshold float64
	// Timeout bounds one assessment. Zero is DefaultRiskTimeout.
	Timeout time.Duration
}

// Assess asks the two pre-execution questions. A failed request is an
// error, so the engine can say the check did not run: it adds nothing.
func (j RiskJudge) Assess(ctx context.Context, command string, threshold float64) (event.Risk, error) {
	if threshold <= 0 {
		threshold = j.Threshold
	}
	if threshold <= 0 {
		threshold = DefaultRiskThreshold
	}
	if j.Asker == nil {
		return event.Risk{}, nil
	}
	timeout := j.Timeout
	if timeout <= 0 {
		timeout = DefaultRiskTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	answers, _, err := j.Asker.Ask(ctx, State(map[string]any{"command": command}), riskQuestions())
	if err != nil {
		return event.Risk{}, err
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
