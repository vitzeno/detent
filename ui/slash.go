package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/event"
)

// Input-bar commands: registry, prefix matching, dropdown. Each entry
// carries its handler, so listed and dispatchable can't drift apart.

// maxSlashRows caps how many entries the dropdown shows at once. Past
// it the list scrolls rather than growing into the history pane.
const maxSlashRows = 6

// slashCmd is one available command. run receives the whole input
// line, so a command can take an argument (/undo 2).
type slashCmd struct {
	name string
	desc string
	run  func(m Model, input string) (Model, tea.Cmd)
	// answers is set when the outcome comes back over the bus.
	answers bool
}

// slashCommands is a function, not a var: /help draws the registry and
// the registry contains /help, which would be an initialisation cycle.
func slashCommands() []slashCmd {
	return []slashCmd{
		{name: "/quit", desc: "quit detent", run: func(m Model, _ string) (Model, tea.Cmd) {
			return m.onQuit("/quit")
		}},
		{name: "/abort", desc: "stop the running request", run: func(m Model, _ string) (Model, tea.Cmd) {
			return m.abortRunning()
		}},
		{name: "/context", desc: "show what fills the model's context, and what to trim", run: func(m Model, _ string) (Model, tea.Cmd) {
			next, cmd := m.openPanel(panelContext)
			// Measured afresh, since a server may have connected since the last Step.
			return next, tea.Batch(cmd, m.send(event.MeasureContext{}))
		}},
		{name: "/status", desc: "show what detent is and what it has done", run: func(m Model, _ string) (Model, tea.Cmd) {
			return m.openPanel(panelStatus)
		}},
		{name: "/sessions", desc: "list the sessions that can be resumed", run: Model.listSessions},
		{name: "/skills", desc: "show the skills this session found", run: func(m Model, _ string) (Model, tea.Cmd) {
			return m.openPanel(panelSkills)
		}},
		{name: "/mcp", desc: "show the MCP servers, or /mcp auth <server> to sign in again", run: Model.listServers},
		{name: "/rename", desc: "name this session, e.g. /rename the sandbox bug",
			run: Model.renameSession, answers: true},
		{name: "/undo", desc: "undo a request and everything after it, e.g. /undo 2", run: Model.runUndo},
		{name: "/delete", desc: "delete a stored session, e.g. /delete the sandbox bug",
			run: Model.runForget, answers: true},
		{name: "/search", desc: "find anything in this session's history and jump to it (ctrl+r)",
			run: func(m Model, input string) (Model, tea.Cmd) {
				return m.openFinder(strings.TrimPrefix(input, "/search"))
			}},
		{name: "/shell", desc: "type commands instead of requests (shift+tab)",
			run: func(m Model, _ string) (Model, tea.Cmd) {
				return m.toggleEntry()
			}},
		{name: "/new", desc: "forget the conversation and start over", run: func(m Model, _ string) (Model, tea.Cmd) {
			return m.startOver()
		}},
		{name: "/help", desc: "show slash commands", run: func(m Model, _ string) (Model, tea.Cmd) {
			return m.openPanel(panelHelp)
		}},
	}
}

// skillCommands are /<name> for each skill the human may ask for. One
// that shares a built-in's name is left out, since the built-in wins.
func skillCommands(skills []event.SkillSummary) []slashCmd {
	var out []slashCmd
	for _, s := range skills {
		// Typed words are matched lowercased, so the name is too.
		name := "/" + strings.ToLower(s.Name)
		if !s.UserInvocable {
			continue
		}
		if _, taken := lookupSlash(name, nil); taken {
			continue
		}
		skill := s.Name
		out = append(out, slashCmd{name: name, desc: "skill: " + s.Description,
			run: func(m Model, input string) (Model, tea.Cmd) { return m.useSkill(skill, input) }})
	}
	return out
}

// useSkill asks the model to load a skill, so a skill asked for by hand
// is a call to the skill tool like any other, and replays as one.
func (m Model) useSkill(name, input string) (Model, tea.Cmd) {
	text := "Load the " + name + " skill and follow it."
	if _, rest, ok := strings.Cut(strings.TrimSpace(input), " "); ok && strings.TrimSpace(rest) != "" {
		text += " The request: " + strings.TrimSpace(rest)
	}
	return m.sendPrompt(text)
}

// runSlash dispatches input to the command its first word names.
func (m Model) runSlash(input string) (Model, tea.Cmd) {
	c, ok := lookupSlash(input, m.skillCmds)
	if !ok {
		m.noteErr("unknown command " + input + " (try /help)")
		return m, nil
	}
	next, cmd := c.run(m, input)
	// Nothing is claimed for a command that has not finished: one that
	// opened a question, or one whose reply comes over the bus.
	if next.notice.text == "" && next.mode == modeInput && !c.answers {
		next.noteOK(c.name)
	}
	return next, cmd
}

func (m Model) acceptSlash() (Model, tea.Cmd) {
	m.prompt.Accept()
	return m, nil
}

// matchSlash returns registry entries with the given input as a
// prefix. Input must start with "/", or nothing matches.
func matchSlash(input string, extra []slashCmd) []slashCmd {
	if !strings.HasPrefix(input, "/") {
		return nil
	}
	var out []slashCmd
	for _, c := range append(slashCommands(), extra...) {
		if strings.HasPrefix(c.name, strings.ToLower(input)) {
			out = append(out, c)
		}
	}
	return out
}

// lookupSlash finds the command named by the first word of input.
func lookupSlash(input string, extra []slashCmd) (slashCmd, bool) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return slashCmd{}, false
	}
	name := strings.ToLower(fields[0])
	for _, c := range append(slashCommands(), extra...) {
		if c.name == name {
			return c, true
		}
	}
	return slashCmd{}, false
}

// slashWindow is the visible slice, derived from the cursor rather than
// stored. The cursor rides the bottom edge once the list has scrolled.
func slashWindow(n, cursor int) (start, end int) {
	if n <= maxSlashRows {
		return 0, n
	}
	start = min(max(0, cursor-maxSlashRows+1), n-maxSlashRows)
	return start, start + maxSlashRows
}

// slashDropdown renders the visible slice of the match list, counting
// what is scrolled out of view so a hidden entry is not taken for absent.
func slashDropdown(cmds []slashCmd, cursor int) string {
	start, end := slashWindow(len(cmds), cursor)
	var b strings.Builder
	for i, c := range cmds[start:end] {
		mark, style := "  ", styleGoal
		if start+i == cursor {
			mark, style = "▸ ", styleRowCursor
		}
		// Pad before styling, or the escape bytes count toward the width.
		label := style.Render(fmt.Sprintf("%-10s", c.name))
		fmt.Fprintf(&b, "  %s%s %s\n", style.Render(mark), label, styleMuted.Render(c.desc))
	}
	if more := slashMoreLine(len(cmds), start, end); more != "" {
		b.WriteString(more + "\n")
	}
	return b.String()
}

// slashMoreLine says what the window is hiding, in whichever
// direction, or "" when the whole list is on screen.
func slashMoreLine(n, start, end int) string {
	var parts []string
	if start > 0 {
		parts = append(parts, fmt.Sprintf("⌃ %d more", start))
	}
	if end < n {
		parts = append(parts, fmt.Sprintf("⌄ %d more", n-end))
	}
	if len(parts) == 0 {
		return ""
	}
	return "    " + styleFaint.Render(strings.Join(parts, " · "))
}
