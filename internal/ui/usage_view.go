package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/vitzeno/detent/internal/ui/status"
	"github.com/vitzeno/detent/internal/usage"
)

// usageLines renders the /usage tool row's content: session totals,
// per-goal rows, and the selected goal's per-step spans — the same
// content the old full-screen overlay showed, now living in the output
// pane like any other focusable entry. Cursor/expand state lives on
// the row (r.usageCursor/usageExpand) rather than the Model, so it's
// per-invocation the same way a table row's own cursor is.
func (m Model) usageLines(r *stepRow) []string {
	goals := m.sess.Tracker().Goals()
	snap := m.sess.Tracker().Snapshot()

	var lines []string
	lines = append(lines, fmt.Sprintf("session · %d goal(s) · %d cmd(s) · %d declined · machine %s · dwell %s · %s proposer tok · %s judge tok",
		snap.Goals, snap.Commands, snap.Declined,
		status.Dur(snap.MachineTime()), status.Dur(snap.Dwell),
		status.Tokens(snap.ProposerTokens), status.Tokens(snap.JudgeTokens)))
	if m.uiPreps > 0 {
		lines = append(lines, fmt.Sprintf("ui prep avg %s over %d refreshes",
			status.Dur(m.uiPrep/time.Duration(m.uiPreps)), m.uiPreps))
	}
	lines = append(lines, "")

	shown := goals
	moreGoals := 0
	if len(shown) > 8 {
		shown = shown[:8]
		moreGoals = len(goals) - 8
	}
	for i, g := range shown {
		mark := "  "
		if i == r.usageCursor {
			mark = styleRowCursor.Render("▸ ")
		}
		ptok, jtok := 0, 0
		for _, s := range g.Steps {
			ptok += s.ProposerPrompt + s.ProposerComplete
			jtok += s.JudgePrompt + s.JudgeComplete
		}
		end := g.End
		if end == "" {
			end = "open"
		}
		lines = append(lines, fmt.Sprintf("%s%d. %s (%s) · %d steps · %s · %s tok",
			mark, i+1, truncateWidth(g.Text, 40), end, len(g.Steps),
			status.Dur(g.MachineTime()), status.Tokens(ptok+jtok)))
		if i == r.usageExpand {
			lines = append(lines, usageSteps(g)...)
		}
	}
	if moreGoals > 0 {
		lines = append(lines, styleFaint.Render(fmt.Sprintf("  … +%d more goals", moreGoals)))
	}
	return lines
}

// usageSteps renders one goal's per-step spans, capped.
func usageSteps(g *usage.Goal) []string {
	var out []string
	steps := g.Steps
	more := 0
	if len(steps) > 10 {
		steps = steps[:10]
		more = len(g.Steps) - 10
	}
	for i, s := range steps {
		var b strings.Builder
		fmt.Fprintf(&b, "    %d. %s", i+1, truncateWidth(s.Command, 30))
		fmt.Fprintf(&b, " · p %s/%s", status.Dur(s.Propose), status.Tokens(s.ProposerPrompt+s.ProposerComplete))
		if s.Dwell > 0 {
			fmt.Fprintf(&b, " · dwell %s", status.Dur(s.Dwell))
		}
		if s.ExitCode >= 0 {
			fmt.Fprintf(&b, " · e %s/%d", status.Dur(s.Exec), s.ExitCode)
		}
		if s.HasPost {
			fmt.Fprintf(&b, " · j %s/%s", status.Dur(s.JudgePost), status.Tokens(s.JudgePrompt+s.JudgeComplete))
			if s.Attention >= 0 {
				fmt.Fprintf(&b, " · att %.2f", s.Attention)
			}
			if s.GoalAchieved >= 0 {
				fmt.Fprintf(&b, " · done %.2f", s.GoalAchieved)
			}
		}
		out = append(out, styleMuted.Render(b.String()))
	}
	if more > 0 {
		out = append(out, styleFaint.Render(fmt.Sprintf("    … +%d more steps", more)))
	}
	return out
}
