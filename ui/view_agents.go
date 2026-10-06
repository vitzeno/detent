package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/island"
	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/status"
)

// The agents block, pinned below history while the running request has
// subagents, so one waiting on the human is seen from anywhere. It stands
// in for their spawn rows, which history draws once the request ends.

const (
	// maxPinned rows show, plus a line counting the rest, so the block has a
	// ceiling however many run.
	maxPinned = 4
	// pinnedName is the most of a name a row gives room to.
	pinnedName = 12
)

// pinned is the running request's subagents, those needing the human first.
func (m Model) pinned() []*agentState {
	if m.cur == nil {
		return nil
	}
	var out []*agentState
	for _, a := range m.agentOrder {
		if slices.Contains(m.cur.rows, a.spawn) {
			out = append(out, a)
		}
	}
	slices.SortStableFunc(out, func(a, b *agentState) int { return cmp.Compare(m.urgency(a), m.urgency(b)) })
	return out
}

// urgency orders the block: what needs the human, then what went wrong,
// then what is still going, then what is done.
func (m Model) urgency(a *agentState) int {
	switch {
	case m.blocked(a):
		return 0
	case a.ended && a.reason != event.AgentDone:
		return 1
	case !a.ended && a.steps > 0:
		return 2
	case !a.ended:
		return 3
	}
	return 4
}

// stoppingNote says a stop was asked for and why the agent has not ended yet.
const stoppingNote = "stopping · writing its report"

// pinnedLines is the block, bordered, or nothing when no subagent runs.
func (m Model) pinnedLines() []string {
	agents := m.pinned()
	if len(agents) == 0 {
		return nil
	}
	width := island.Inner(m.layout.histColW) - 2
	inner := island.Inner(width)
	shown := agents[:min(len(agents), maxPinned)]
	rows := make([]string, 0, len(shown)+1)
	for _, a := range shown {
		rows = append(rows, m.pinnedRow(a, inner))
	}
	if rest := agents[len(shown):]; len(rest) > 0 {
		rows = append(rows, styleFaint.Render(layout.Truncate(fmt.Sprintf("+%d more", len(rest)), inner)))
	}
	return strings.Split(island.Render("", palette.Border, rows, width, len(rows)), "\n")
}

// pinnedRow is one agent: how it stands, its name, and how full its context
// is, or how it ended.
func (m Model) pinnedRow(a *agentState, width int) string {
	mark := "  "
	style := toolName.agent
	if reviewerOf(a) != nil {
		style = toolName.review
	}
	name := style.Render(fmt.Sprintf("%-*s", pinnedName, layout.Truncate(a.name, pinnedName)))
	head := mark + m.agentGlyph(a) + " " + name + " "
	room := max(0, width-lipgloss.Width(head))
	if a.ended {
		return head + styleFaint.Render(layout.Truncate(m.endedDetail(a), room))
	}
	if a.stopping {
		return head + styleCaution.Render(layout.Truncate(stoppingNote, room))
	}
	// A reviewer's progress is the diff it has read, not how full its context is.
	if r := reviewerOf(a); r != nil {
		read := min(len(a.read), r.files)
		label := fmt.Sprintf(" %d/%d ✎%d", read, r.files, r.comments)
		pct := 0
		if r.files > 0 {
			pct = read * 100 / r.files
		}
		return head + m.contextBar(a, room-lipgloss.Width(label), pct) + styleMuted.Render(label)
	}
	pct := m.agentContext(a)
	label := fmt.Sprintf(" %3d%%", pct)
	return head + m.contextBar(a, room-len(label), pct) + styleMuted.Render(label)
}

// reviewerOf is the review row a reviewer stands in for, nil for any other agent.
func reviewerOf(a *agentState) *historyRow {
	if a.spawn != nil && a.spawn.review != uuid.Nil {
		return a.spawn
	}
	return nil
}

// reviewGlyph breathes, filled then hollow, so a reviewer reads as its own thing.
func (m Model) reviewGlyph() string {
	if m.pulse/4%2 == 0 {
		return toolName.review.Render("◆")
	}
	return toolName.review.Render("◇")
}

// agentGlyph says how an agent stands at a glance.
func (m Model) agentGlyph(a *agentState) string {
	switch {
	case m.blocked(a):
		return styleCaution.Render("!")
	case !a.ended && a.stopping:
		return styleCaution.Render("⊘")
	case !a.ended && a.steps == 0:
		return styleFaint.Render("·")
	case !a.ended && reviewerOf(a) != nil:
		return m.reviewGlyph()
	case !a.ended:
		return m.spinner.View()
	case a.reason == event.AgentDone:
		return styleSafe.Render("✓")
	case a.reason == event.AgentPartial:
		return styleCaution.Render("◐")
	case a.reason == event.AgentStopped:
		return styleFaint.Render("⊘")
	}
	return styleDanger.Render("✗")
}

// endedDetail is how an agent ended and how long it ran.
func (m Model) endedDetail(a *agentState) string {
	if a.spawn != nil && a.spawn.took > 0 {
		return string(a.reason) + " " + status.Dur(a.spawn.took)
	}
	return string(a.reason)
}

// agentContext is how full an agent's context is, as a percentage of its budget:
// a child's share of the window, or for a reviewer, which reads a whole diff, all of it.
func (m Model) agentContext(a *agentState) int {
	budget := m.run.ChildContextTokens
	if reviewerOf(a) != nil {
		budget = m.run.ContextTokens
	}
	if budget <= 0 {
		return 0
	}
	return min(100, a.ctx*100/budget)
}

// contextBar fills pct of width, in caution while the agent waits on the human.
func (m Model) contextBar(a *agentState, width, pct int) string {
	if width <= 0 {
		return ""
	}
	fill := width * pct / 100
	style := styleBrand
	if m.blocked(a) {
		style = styleCaution
	}
	return style.Render(strings.Repeat("━", fill)) + styleFaint.Render(strings.Repeat("━", width-fill))
}
