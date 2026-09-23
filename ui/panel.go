package ui

import (
	"fmt"
	"github.com/vitzeno/detent/event"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/ui/status"
	"github.com/vitzeno/detent/version"
)

// The read-only pages about the session rather than one call. An
// overlay, not a block, so opening one leaves history alone.

func (m Model) openPanel(k panelKind) (tea.Model, tea.Cmd) {
	m.panel.open = k
	m.nav.focus = focusOutput
	m.output.GotoTop()
	return m, nil
}

func (m *Model) closePanel() bool {
	if m.panel.open == panelNone {
		return false
	}
	m.panel.open = panelNone
	m.nav.focus = focusInput
	m.prompt.Focus()
	return true
}

// panelLines draws the open page, through the viewport, so length is
// scrolling rather than a cap.
func (m *Model) panelLines() []string {
	switch m.panel.open {
	case panelUsage:
		return m.usageLines()
	case panelStatus:
		return m.statusLines()
	case panelHelp:
		return m.helpLines()
	case panelSessions:
		return m.sessionLines()
	}
	return nil
}

func (m *Model) usageLines() []string {
	out := []string{styleGoal.Render("usage"), ""}
	for _, b := range m.blocks {
		head := fmt.Sprintf("  %s  %s", styleFaint.Render(fmt.Sprintf("#%d", b.n)),
			truncCell(b.prompt, m.layout.outputColW-30))
		out = append(out, head)
		out = append(out, styleFaint.Render(fmt.Sprintf("      %d call(s) · %s tok · %s",
			len(b.rows), status.Tokens(b.used.Tokens()), b.end)))
	}
	if len(m.blocks) == 0 {
		out = append(out, styleFaint.Render("  (nothing yet)"))
	}
	return append(out, "", styleFaint.Render(fmt.Sprintf("  %d step(s) · %s tok total",
		m.steps, status.Tokens(m.tokens))))
}

// sessionLines is what can be resumed. Read-only, because resuming
// is a process rather than a keystroke; the id is here to be copied.
func (m *Model) sessionLines() []string {
	out := []string{styleGoal.Render("sessions"), "",
		styleFaint.Render("  resume one with  detent -resume <id or name>"), ""}
	for _, s := range m.sessions {
		mark := "  "
		if s.ID == m.run.Session {
			mark = styleGoal.Render("▸ ")
		}
		name := styleFaint.Render(s.Model)
		if s.Name != "" {
			name = styleGoal.Render(s.Name)
		}
		out = append(out, fmt.Sprintf("%s%s  %s  %s  %s", mark,
			styleGoal.Render(s.ID.String()),
			styleFaint.Render(s.Started.Local().Format("2006-01-02 15:04")),
			styleFaint.Render(pad(countOf(s.Events, "event"), 12)), name))
	}
	if len(m.sessions) == 0 {
		out = append(out, styleFaint.Render("  (nothing recorded yet)"))
	}
	return out
}

func (m *Model) statusLines() []string {
	rows := [][2]string{
		{"version", version.String()},
		{"model", m.run.Model},
		{"judge", orNone(m.run.Judge)},
		{"runs in", m.runMode()},
		{"step bound", fmt.Sprint(m.run.MaxSteps)},
		{"", ""},
		{"session", m.run.Session.String()},
		{"recording", recording(m.run)},
		{"resumed", resumed(m.run)},
		{"on disk", countOf(len(m.sessions), "session")},
		{"", ""},
		{"requests", fmt.Sprint(len(m.blocks))},
		{"steps", fmt.Sprint(m.steps)},
		{"calls", fmt.Sprint(m.calls)},
		{"errors", fmt.Sprint(m.errors)},
		{"views drawn", fmt.Sprint(m.views)},
		{"tokens", status.Tokens(m.tokens)},
	}
	out := []string{styleGoal.Render("status"), ""}
	for _, r := range rows {
		if r[0] == "" {
			out = append(out, "")
			continue
		}
		out = append(out, fmt.Sprintf("  %s  %s", styleFaint.Render(pad(r[0], 12)), r[1]))
	}
	return out
}

func (m *Model) helpLines() []string {
	out := []string{styleGoal.Render("commands"), ""}
	for _, c := range slashCommands() {
		out = append(out, fmt.Sprintf("  %s  %s", styleGoal.Render(pad(c.Name, 12)),
			styleFaint.Render(c.Desc)))
	}
	return append(out, "", styleGoal.Render("keys"), "",
		"  "+styleFaint.Render("tab       move between input, history and output"),
		"  "+styleFaint.Render("↑ ↓       move the cursor, or scroll the output"),
		"  "+styleFaint.Render("space     expand a call's output inline"),
		"  "+styleFaint.Render("enter     seed the prompt from a view's selection"),
		"  "+styleFaint.Render("esc       back out, or abort a running request"),
		"  "+styleFaint.Render("ctrl+c    quit"))
}

// recording says so when nothing is writing this down, because the
// alternative is finding out at resume time.
func recording(run event.SessionStarted) string {
	if run.Recorded {
		return styleSafe.Render("● yes") + styleFaint.Render("  resumable")
	}
	return styleDanger.Render("✗ no") + styleFaint.Render("  this session cannot be resumed")
}

func resumed(run event.SessionStarted) string {
	if run.Resumed == 0 {
		return styleFaint.Render("(a new session)")
	}
	return fmt.Sprintf("%s %s", countOf(run.Resumed, "record"),
		styleFaint.Render("restored"))
}

func countOf(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

func orNone(s string) string {
	if s == "" {
		return styleFaint.Render("(none)")
	}
	return s
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func truncCell(s string, w int) string {
	if w < 8 {
		w = 8
	}
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) <= w {
		return string(r)
	}
	return string(r[:w-1]) + "…"
}
