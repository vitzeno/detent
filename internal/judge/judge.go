// Package judge reads a finished Call: how it went, how to draw it,
// and whether the request now looks answered. A bus subscriber, so
// none of it gates execution.
package judge

import (
	"context"
	"github.com/google/uuid"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/viewgen"
)

// Result status, as the UI's own status package spells them.
const (
	StatusClean    = "clean_success"
	StatusWarnings = "success_with_warnings"
	StatusFailed   = "failed"
	StatusEmpty    = "empty"
)

// GoalMet is where the judge's goal-achieved score stops being an
// opinion and becomes a reason to stop asking for tools.
const GoalMet = 0.9

// ResultJudge judges one finished Call.
type ResultJudge struct {
	Asker classify.Asker
	// Prompt is the request being worked, which goal_achieved needs.
	// Kept per Turn by Watch.
	Prompt string
}

// Watch judges every finished Call and publishes what it decided. A
// high goal-achieved becomes a RequestStop, honoured at the next Step
// boundary — acting on a verdict without intercepting.
func Watch(bus *event.Bus, asker classify.Asker) func() {
	facts, stop := bus.Subscribe(event.Only(
		event.TurnStartedKind, event.CallProposedKind, event.CallEndedKind))
	go func() {
		var prompt string
		var turn uuid.UUID
		cmds := map[uuid.UUID]string{}
		for rec := range facts {
			switch v := rec.Event.(type) {
			case event.TurnStarted:
				prompt, turn = v.Prompt, v.Turn
				clear(cmds)
			case event.CallProposed:
				cmds[v.Call] = event.Command(v.Tool, v.Args)
			case event.CallEnded:
				j := ResultJudge{Asker: asker, Prompt: prompt}
				go j.publish(bus, turn, v, cmds[v.Call])
			}
		}
	}()
	return stop
}

// publish judges one Call. Each runs on its own goroutine: several
// Calls finish together, and one slow judgement must not hold up the
// others or the events behind them.
func (j ResultJudge) publish(bus *event.Bus, turn uuid.UUID, done event.CallEnded, command string) {
	got := j.Judge(context.Background(), command, done.Result)
	got.Call = done.Call
	bus.Publish(got)
	if got.FromJudge && got.GoalAchieved >= GoalMet {
		bus.Publish(event.RequestStop{Turn: turn,
			Reason: "the judge reads the request as answered"})
	}
}

// Judge asks, and falls back to a heuristic when nothing answers. The
// fallback is why FromJudge exists: the UI must never present a guess
// as a verdict.
func (j ResultJudge) Judge(ctx context.Context, command string, res event.Result) event.CallJudged {
	out := heuristic(res)
	answers, _, ok := classify.AskOrFallback(ctx, j.Asker,
		classify.State(map[string]any{"request": j.Prompt, "command": command, "output": output(res),
			"exit_code": res.ExitCode}),
		resultQuestions())
	if !ok {
		return out
	}
	out.FromJudge = true
	if a, has := answers["result_status"]; has && a.Choice != "" {
		out.Status = a.Choice
	}
	if a, has := answers["render_kind"]; has && a.Choice != "" {
		out.RenderKind = a.Choice
	}
	if a, has := answers["attention"]; has {
		out.Attention = a.Noul
	}
	if a, has := answers["goal_achieved"]; has {
		out.GoalAchieved = a.Noul
	}
	return out
}

// heuristic is what a row reads as with no judge wired. Deliberately
// dull: it never claims a shape, only that something failed or was
// silent.
func heuristic(res event.Result) event.CallJudged {
	out := event.CallJudged{RenderKind: viewgen.KindText, GoalAchieved: -1}
	switch {
	case res.Err != "" || res.ExitCode != 0:
		out.Status, out.RenderKind, out.Attention = StatusFailed, viewgen.KindError, 0.9
	case strings.TrimSpace(output(res)) == "":
		out.Status, out.Attention = StatusEmpty, 0.1
	default:
		out.Status, out.Attention = StatusClean, 0.1
	}
	return out
}

func resultQuestions() classify.Questions {
	return classify.Questions{
		"result_status": {
			Instructions: "Given the tool call and its captured output, how did it go? " +
				"Warnings means it succeeded but the output contains errors, retries or deprecations worth noticing.",
			Choice: &classify.ChoiceQuestion{Criteria: map[string]any{
				StatusClean:    "succeeded with clean, expected output",
				StatusWarnings: "succeeded but output contains warnings, errors or retries worth noticing",
				StatusFailed:   "failed: non-zero exit, or output showing it did not do its job",
				StatusEmpty:    "ran fine but produced no useful output",
			}},
		},
		"render_kind": {
			Instructions: "What shape is this output? Pick how a human should read it.",
			Choice:       &classify.ChoiceQuestion{Criteria: viewgen.RenderKindCriteria()},
		},
		"attention": {
			Instructions: "Does this outcome need the human's attention before continuing — " +
				"a failure, a surprise, a destructive result — rather than collapsing to one line?",
			Noul: &classify.NoulQuestion{},
		},
		"goal_achieved": {
			Instructions: "Has the human's request been answered given everything observed so far? " +
				"A second opinion: another model is choosing the tools, so judge only whether " +
				"the request itself now looks satisfied.",
			Noul: &classify.NoulQuestion{},
		},
	}
}

// output caps what the judge is shown. A judgement is about shape and
// outcome, neither of which needs a megabyte.
func output(r event.Result) string {
	var b strings.Builder
	b.WriteString(r.Stdout)
	if r.Stderr != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(r.Stderr)
	}
	const max = 4 * 1024
	if s := b.String(); len(s) > max {
		return s[:max] + "\n…[truncated]"
	}
	return b.String()
}
