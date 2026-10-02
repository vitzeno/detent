package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/status"
)

// contextLines is /context: one bar for the budget, then what every Step
// resends and the history compaction folds, each saying what made it big.
func (m *Model) contextLines() []string {
	c := m.measured
	out := []string{styleGoal.Render("context"), ""}
	if c.Budget <= 0 {
		return append(out, styleFaint.Render("  (nothing measured yet)"))
	}
	width := max(m.layout.outputColW-4, 20)
	fixed, history := partsSum(c.Fixed), partsSum(c.History)

	head := fmt.Sprintf("  %s of %s · %d%%", tok(c.Total), tok(c.Budget), c.Total*100/c.Budget)
	if !c.Exact {
		head += styleFaint.Render("  estimated until the next step")
	}
	out = append(out, head, "  "+contextBar(fixed, history, c.Budget, width), "")

	rate := ""
	if c.Growth > 0 {
		rate = ", +" + tok(c.Growth) + " a step"
	}
	out = append(out, styleMuted.Render(fmt.Sprintf("  fixed %s, resent every step · history %s%s",
		tok(fixed), tok(history), rate)))
	out = append(out, styleFaint.Render("  "+compactsWhen(history, c.Budget, c.Growth)), "")

	largest := 1
	for _, p := range append(append([]event.ContextPart(nil), c.Fixed...), c.History...) {
		largest = max(largest, p.Tokens)
	}
	out = append(out, styleGoal.Render("fixed")+styleFaint.Render("  ·  the same on every step"))
	for _, p := range c.Fixed {
		detail := p.Detail
		if p.Tokens*2 > fixed {
			detail += ", most of what is fixed"
		}
		out = append(out, partRow(p.Name, p.Tokens, largest, detail, width))
	}
	out = append(out, "", styleGoal.Render("history")+styleFaint.Render("  ·  oldest first, the order compaction folds it"))
	if len(c.History) == 0 {
		out = append(out, styleFaint.Render("  (nothing yet)"))
	}
	for _, p := range c.History {
		out = append(out, partRow(historyName(p), p.Tokens, largest, historyDetail(p), width))
	}
	return append(out, "", styleFaint.Render(fmt.Sprintf("  this session  %d steps · %s tokens sent",
		m.steps, tok(m.tokens))))
}

// contextBar is the whole budget: what is fixed, what is history, and
// what is free, each cell an equal share.
func contextBar(fixed, history, budget, width int) string {
	cells := func(n int) int { return min(width, (n*width+budget/2)/budget) }
	f := cells(fixed)
	h := min(width-f, cells(fixed+history)-f)
	return styleBrand.Render(strings.Repeat("█", f)) +
		styleMuted.Render(strings.Repeat("█", h)) +
		styleFaint.Render(strings.Repeat("·", width-f-h))
}

// compactsWhen says where history is folded and, at the current rate,
// how many Steps away that is.
func compactsWhen(history, budget, growth int) string {
	s := "history compacts at " + tok(budget)
	if growth > 0 && history < budget {
		s += fmt.Sprintf(", about %d steps away", (budget-history)/growth)
	}
	return s
}

// partRow is a name, its size, a bar against the largest part, and why.
func partRow(name string, tokens, largest int, detail string, width int) string {
	const nameW, barW = 20, 12
	row := fmt.Sprintf("  %s %7s  %s  ", padWidth(ansi.Truncate(name, nameW, "…"), nameW),
		tok(tokens), padWidth(miniBar(tokens, largest, barW), barW))
	return row + styleFaint.Render(ansi.Truncate(detail, max(width-ansi.StringWidth(row), 8), "…"))
}

// miniBar draws n of most across width cells, in eighths.
func miniBar(n, most, width int) string {
	eighths := n * width * 8 / most
	if n > 0 && eighths == 0 {
		eighths = 1
	}
	return strings.Repeat("█", eighths/8) + []string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}[eighths%8]
}

func historyName(p event.ContextPart) string {
	if p.N == 0 {
		return p.Name
	}
	return fmt.Sprintf("#%d %s", p.N, firstLine(p.Name))
}

// historyDetail says why a request is the size it is, when one message
// is a good part of it, or that it is safe from compaction.
func historyDetail(p event.ContextPart) string {
	switch {
	case p.Open:
		return "running, kept until it ends"
	case p.Largest == "a reply" && p.LargestTokens*3 >= p.Tokens:
		return "a reply of " + tok(p.LargestTokens)
	case p.Largest != "" && p.LargestTokens*3 >= p.Tokens:
		return firstLine(p.Largest) + " printed " + tok(p.LargestTokens)
	}
	return p.Detail
}

// padWidth pads s to n cells, counting what shows rather than bytes.
func padWidth(s string, n int) string {
	return s + strings.Repeat(" ", max(n-ansi.StringWidth(s), 0))
}

// tok is a token count without a trailing ".0", so a budget reads 200k.
func tok(n int) string {
	return strings.Replace(status.Tokens(n), ".0", "", 1)
}

func partsSum(parts []event.ContextPart) int {
	n := 0
	for _, p := range parts {
		n += p.Tokens
	}
	return n
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
