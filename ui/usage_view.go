package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/status"
)

// usageLines renders the /usage panel: session totals, per-goal rows,
// and the selected goal's per-step spans. Cursor and expand come from
// the panel, since /usage is a page about the session, not a step.
func (m Model) usageLines(cursor, expand int) []string {
	goals := m.sess.Tracker()
	snap := m.sess.UsageSnapshot()

	var lines []string
	lines = append(lines, fmt.Sprintf("session · %d goal(s) · %d cmd(s) · %d declined · machine %s · dwell %s · %s proposer tok · %s judge tok",
		snap.Goals, snap.Commands, snap.Declined,
		status.Dur(snap.MachineTime()), status.Dur(snap.Dwell),
		status.Tokens(snap.ProposerTokens), status.Tokens(snap.JudgeTokens)))
	if m.perf.uiPreps > 0 {
		lines = append(lines, fmt.Sprintf("ui prep avg %s over %d refreshes",
			status.Dur(m.perf.uiPrep/time.Duration(m.perf.uiPreps)), m.perf.uiPreps))
	}
	lines = append(lines, "")

	// Every goal, not the first eight: the panel is a scrolling
	// viewport, so a cap here would hide what scrolling is for.
	for i, g := range goals {
		mark := "  "
		if i == cursor {
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
			mark, i+1, layout.Truncate(g.Text, 40), end, len(g.Steps),
			status.Dur(g.MachineTime()), status.Tokens(ptok+jtok)))
		if i == expand {
			lines = append(lines, usageSteps(g)...)
		}
	}
	return lines
}

// usageSteps renders one goal's per-step spans. Uncapped: the
// panel scrolls.
func usageSteps(g GoalStats) []string {
	var out []string
	for i, s := range g.Steps {
		var b strings.Builder
		fmt.Fprintf(&b, "    %d. %s", i+1, layout.Truncate(s.Command, 30))
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
	return out
}
