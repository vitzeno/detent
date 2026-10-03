package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/status"
	"github.com/vitzeno/detent/version"
)

// The read-only pages about the session rather than one call. An
// overlay, not a block, so opening one leaves history alone.

func (m Model) openPanel(k panelKind) (Model, tea.Cmd) {
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
	case panelContext:
		return m.contextLines()
	case panelStatus:
		return m.statusLines()
	case panelHelp:
		return m.helpLines()
	case panelSessions:
		return m.sessionLines()
	case panelMCP:
		return m.mcpLines()
	case panelSkills:
		return m.skillLines()
	default:
		return nil
	}
}

func (m *Model) statusLines() []string {
	rows := [][2]string{
		{"version", version.String()},
		{"model", m.run.Model},
		{"judge", orNone(m.run.Judge)},
		{"runs in", m.runMode()},
		{"step bound", strconv.Itoa(m.run.MaxSteps)},
		{"instructions", orNone(strings.Join(m.run.Instructions, ", "))},
		{"", ""},
		{"session", m.run.Session.String()},
		{"recording", recording(m.run)},
		{"resumed", resumed(m.run)},
		{"on disk", countOf(len(m.sessions), "session")},
		{"", ""},
		{"requests", strconv.Itoa(len(m.blocks))},
		{"steps", strconv.Itoa(m.steps)},
		{"tool calls", strconv.Itoa(m.calls)},
		{"errors", strconv.Itoa(m.errors)},
		{"views drawn", strconv.Itoa(m.views)},
		{"tokens", status.Tokens(m.tokens)},
		{"context", m.contextDetail()},
	}
	out := []string{styleGoal.Render("status"), ""}
	for _, r := range rows {
		if r[0] == "" {
			out = append(out, "")
			continue
		}
		out = append(out, fmt.Sprintf("  %s  %s", styleFaint.Render(padWidth(r[0], 12)), r[1]))
	}
	return out
}

// contextDetail spells out what the bar compresses to a percentage,
// which is the wrong thing when you want to know how much room is left.
func (m *Model) contextDetail() string {
	budget := m.run.ContextTokens
	if budget <= 0 {
		return "no budget set"
	}
	if m.ctxTokens <= 0 {
		return "nothing measured yet, budget " + status.Tokens(budget)
	}
	return fmt.Sprintf("%d%%  %s of %s", m.ctxTokens*100/budget,
		status.Tokens(m.ctxTokens), status.Tokens(budget))
}

func (m *Model) helpLines() []string {
	out := []string{styleGoal.Render("commands"), ""}
	for _, c := range append(slashCommands(), m.skillCmds...) {
		out = append(out, fmt.Sprintf("  %s  %s", styleGoal.Render(padWidth(c.name, 12)),
			styleFaint.Render(c.desc)))
	}
	return append(out, "", styleGoal.Render("keys"), "",
		"  "+styleFaint.Render("tab       move between input, history and output"),
		"  "+styleFaint.Render("↑ ↓       move the cursor, or scroll the output"),
		"  "+styleFaint.Render("end       jump to the newest row and follow it again"),
		"  "+styleFaint.Render("space     expand a tool call's output inline"),
		"  "+styleFaint.Render("enter     seed the prompt from a view's selection"),
		"  "+styleFaint.Render("esc       back out, or abort a running request"),
		"  "+styleFaint.Render("ctrl+c    quit"))
}

// sessionLines is what can be resumed. Read-only, because resuming
// is a process rather than a keystroke. The id is here to be copied.
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
			styleFaint.Render(padWidth(countOf(s.Events, "event"), 12)), name))
	}
	if len(m.sessions) == 0 {
		out = append(out, styleFaint.Render("  (nothing recorded yet)"))
	}
	return out
}

// mcpLines draws what connected, what failed, and why. Colour carries
// the state: it is what a human opens this page to see.
func (m *Model) mcpLines() []string {
	out := []string{styleGoal.Render("mcp servers"), ""}

	if len(m.servers) == 0 {
		return append(out, styleFaint.Render("  (none configured, see .mcp.json)"))
	}
	for _, s := range m.servers {
		out = append(out, fmt.Sprintf("  %s %s  %s",
			serverMark(s), styleGoal.Render(padWidth(s.Name, 14)), serverState(s)))
		if s.Command != "" {
			out = append(out, styleFaint.Render("     "+s.Command))
		}
		if s.Err != "" {
			out = append(out, styleDanger.Render("     "+s.Err))
		}
	}
	return append(out, "", styleFaint.Render("  [esc] close"))
}

// skillLines lists the skills found at startup, and who may ask for each.
func (m *Model) skillLines() []string {
	out := []string{styleGoal.Render("skills"), ""}
	if len(m.run.Skills) == 0 {
		return append(out, styleFaint.Render("  (none found, add one under .agents/skills/<name>/SKILL.md)"))
	}
	for _, s := range m.run.Skills {
		where := "personal"
		if s.Project {
			where = "project"
		}
		ask := "/" + s.Name
		if !s.UserInvocable {
			ask = "model only"
		}
		out = append(out, "  "+styleGoal.Render(s.Name)+styleFaint.Render("  "+where+" · "+ask),
			"    "+truncCell(s.Description, m.layout.outputColW-6), "")
	}
	return out
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

// serverMark is the glyph the eye lands on first: connected, off,
// still dialling, or broken.
func serverMark(s event.ServerSummary) string {
	switch {
	case s.Disabled:
		return styleFaint.Render("○")
	case s.Err != "":
		return styleDanger.Render("✗")
	case !s.Connected:
		return styleFaint.Render("◌")
	case s.Tools == 0:
		return styleCaution.Render("●")
	}
	return styleSafe.Render("●")
}

// serverState names what the glyph means: a colour alone is not one.
func serverState(s event.ServerSummary) string {
	switch {
	case s.Disabled:
		return styleFaint.Render("disabled")
	case s.Auth == event.AuthWaiting:
		return styleCaution.Render("waiting for you to sign in")
	case s.Auth == event.AuthSignedOut:
		return styleCaution.Render("signed out · /mcp auth " + s.Name)
	case s.Err != "":
		return styleDanger.Render("not connected")
	case !s.Connected:
		return styleFaint.Render("connecting…")
	case s.Tools == 0:
		return styleCaution.Render("connected, offers nothing")
	}
	return styleSafe.Render(countOf(s.Tools, "tool"))
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

// truncCell fits a cell by display width, since a session name or a
// skill's description is whatever someone typed.
func truncCell(s string, w int) string {
	return layout.Truncate(strings.Join(strings.Fields(s), " "), max(w, 8))
}
