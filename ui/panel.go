package ui

import (
	"fmt"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/ui/status"
	"github.com/vitzeno/detent/version"
)

// Panels: read-only pages about the session rather than about a step.
//
// They take the output pane without touching history. A panel is not
// something the harness did, so filing one as a history entry put a
// row where the running goal's own row should be, and there is no
// undoing or re-reading it later. Being an overlay also means the
// viewport draws them, so a long one scrolls rather than being cut at
// whatever count fitted.

const (
	panelUsage  = "usage"
	panelStatus = "status"
	panelHelp   = "help"
)

// panelState is the open panel, or the zero value for none. cursor and
// expand belong to usage, the only panel with rows to move through.
type panelState struct {
	kind   string
	cursor int
	expand int
}

func (p panelState) open() bool { return p.kind != "" }

// openPanel replaces whatever panel was open, so /usage after /status
// swaps rather than stacking.
func (m Model) openPanel(kind string) (tea.Model, tea.Cmd) {
	m.panel = panelState{kind: kind, expand: -1}
	m.nav.focus = focusOutput
	// Filled here rather than left to the next refresh: opening a
	// panel is what should show it, and a caller that renders without
	// updating first would otherwise see the pane it replaced.
	m.setViewContent(strings.Join(m.panelLines(), "\n"))
	m.output.GotoTop()
	return m, nil
}

func (m Model) closePanel() Model {
	m.panel = panelState{}
	m.nav.focus = focusInput
	return m
}

// panelKey routes a keystroke to the open panel. Only usage takes one;
// everything else scrolls, which the caller does.
func (m Model) panelKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if m.panel.kind != panelUsage {
		return m, nil, false
	}
	switch msg.String() {
	case "up", "k":
		if m.panel.cursor > 0 {
			m.panel.cursor--
		}
		return m, nil, true
	case "down", "j":
		if m.panel.cursor < len(m.sess.Tracker())-1 {
			m.panel.cursor++
		}
		return m, nil, true
	case "enter", " ":
		if m.panel.expand == m.panel.cursor {
			m.panel.expand = -1
		} else {
			m.panel.expand = m.panel.cursor
		}
		return m, nil, true
	}
	return m, nil, false
}

// panelLines is what the open panel draws. Whole, not windowed: the
// viewport does that.
func (m Model) panelLines() []string {
	switch m.panel.kind {
	case panelUsage:
		return m.usageLines(m.panel.cursor, m.panel.expand)
	case panelStatus:
		return m.statusLines()
	case panelHelp:
		return helpLines()
	}
	return nil
}

// statusLines is what detent is, right now: the build, what it talks
// to, where commands run, and what the session has done. The welcome
// pane says most of this before anything has run; this is the same
// question asked later, when it has.
func (m Model) statusLines() []string {
	snap := m.sess.UsageSnapshot()
	var out []string
	add := func(label, value string) {
		out = append(out, fmt.Sprintf("  %-16s %s", styleFaint.Render(label), value))
	}
	head := func(s string) {
		out = append(out, "", styleBrand.Render(s))
	}

	out = append(out, styleBrand.Render("detent ")+styleGoal.Render(version.String()))

	head("models")
	add("proposer", styleGoal.Render(m.info.Proposer))
	if m.info.Judge == "" {
		add("judge", styleFaint.Render("none, so rows fall back to heuristics"))
	} else {
		add("judge", styleGoal.Render(m.info.Judge))
	}

	head("where commands run")
	if m.info.RunMode == "sandbox" {
		add("mode", styleSafe.Render("● sandboxed, in containerd"))
		add("image", styleGoal.Render(m.info.Image))
		add("workspace", styleGoal.Render(m.info.Mount))
		add("network", styleGoal.Render(m.info.Network))
	} else {
		add("mode", styleCaution.Render("⚠ on this host, unsandboxed"))
	}
	add("machine", styleGoal.Render(fmt.Sprintf("%s/%s · %d cpu",
		runtime.GOOS, runtime.GOARCH, runtime.NumCPU())))

	head("this session")
	add("goals", styleGoal.Render(fmt.Sprintf("%d", snap.Goals)))
	add("commands", styleGoal.Render(fmt.Sprintf("%d", snap.Commands)))
	add("declined", styleGoal.Render(fmt.Sprintf("%d", snap.Declined)))
	add("failed", countStyle(m.counts.failed))
	add("machine time", styleGoal.Render(status.Dur(snap.MachineTime())))
	add("your time", styleGoal.Render(status.Dur(snap.Dwell)))
	add("tokens", styleGoal.Render(fmt.Sprintf("%s proposer · %s judge",
		status.Tokens(snap.ProposerTokens), status.Tokens(snap.JudgeTokens))))

	head("views")
	add("mode", styleGoal.Render(m.info.Views))
	add("composed", styleGoal.Render(fmt.Sprintf("%d", m.counts.composed)))
	add("reused", styleGoal.Render(fmt.Sprintf("%d saved · %d shipped",
		m.counts.saved, m.counts.shipped)))
	add("declined", styleGoal.Render(fmt.Sprintf("%d", m.counts.viewDeclined)))

	return out
}

// countStyle draws a count of things that went wrong, in the colour
// that means it. Zero is not a warning.
func countStyle(n int) string {
	if n == 0 {
		return styleGoal.Render("0")
	}
	return styleDanger.Render(fmt.Sprintf("%d", n))
}

// counters are what /status reports beyond what usage already tracks.
// Views are counted here rather than in viewgen because the pane is
// what learns the answer: a spec may be composed once and drawn for
// several rows.
type counters struct {
	failed       int
	composed     int
	saved        int
	shipped      int
	viewDeclined int
}

// countView records where a row's view came from, once per row.
func (m *Model) countView(src ViewSource) {
	switch src {
	case ViewGenerated:
		m.counts.composed++
	case ViewSaved:
		m.counts.saved++
	case ViewShipped:
		m.counts.shipped++
	case ViewDeclined:
		m.counts.viewDeclined++
	}
}
