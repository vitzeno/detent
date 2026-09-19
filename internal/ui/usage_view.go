package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vitzeno/detent/internal/ui/island"
	"github.com/vitzeno/detent/internal/ui/status"
	"github.com/vitzeno/detent/internal/usage"
)

// usageOverlay renders the drilldown: session totals, per-goal rows,
// and the selected goal's per-step spans. Boxed and centered over the
// normal screen; esc closes.
func (m Model) usageOverlay() string {
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
		if i == m.usageCursor {
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
		if i == m.usageExpand {
			lines = append(lines, usageSteps(g)...)
		}
	}
	if moreGoals > 0 {
		lines = append(lines, styleFaint.Render(fmt.Sprintf("  … +%d more goals", moreGoals)))
	}
	lines = append(lines, "", styleHint.Render("[j/k] goal · [enter] expand · [esc] close"))

	boxW := min(100, max(40, m.width-4))
	boxH := min(len(lines)+2, max(8, m.height-2))
	box := islandBox(lines, boxW, boxH)
	return spliceCentered(strings.Split(m.baseView(), "\n"), box, m.width, m.height)
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

// usageKey navigates the overlay: goals, expand, close.
func (m Model) usageKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.sess.Tracker().Goals())
	switch msg.String() {
	case "esc", "q":
		m.showUsage = false
		return m, nil
	case "up", "k":
		if m.usageCursor > 0 {
			m.usageCursor--
		}
		return m, nil
	case "down", "j":
		if m.usageCursor < n-1 {
			m.usageCursor++
		}
		return m, nil
	case "enter":
		if m.usageExpand == m.usageCursor {
			m.usageExpand = -1
		} else {
			m.usageExpand = m.usageCursor
		}
		return m, nil
	}
	return m, nil
}

// spliceCentered overlays box lines (already full-width) onto the
// middle of base lines.
func spliceCentered(base, box []string, width, height int) string {
	for len(base) < height {
		base = append(base, "")
	}
	top := (height - len(box)) / 2
	if top < 0 {
		top = 0
	}
	for i, line := range box {
		if top+i < len(base) {
			base[top+i] = centerLine(line, width)
		}
	}
	return strings.Join(base[:height], "\n")
}

func centerLine(line string, width int) string {
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, line)
}

// islandBox borders content like the zone islands.
func islandBox(lines []string, width, height int) []string {
	for len(lines) < height-2 {
		lines = append(lines, "")
	}
	if len(lines) > height-2 {
		lines = lines[:height-2]
	}
	return strings.Split(island.Render("", false, lines, width, height-2), "\n")
}
