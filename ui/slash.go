package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Input-bar commands: registry, prefix matching, dropdown. Each entry
// carries its handler, so listed and dispatchable can't drift apart.

// slashCmd is one available command. run receives the whole input
// line, so a command can take an argument (/rollback 2).
type slashCmd struct {
	Name string
	Desc string
	run  func(m Model, input string) (tea.Model, tea.Cmd)
}

var slashCommands = []slashCmd{
	{"/quit", "quit detent", func(m Model, _ string) (tea.Model, tea.Cmd) {
		return m, tea.Quit
	}},
	{"/abort", "abort the running command", Model.abortRunning},
	{"/tree", "browse files and directories", func(m Model, _ string) (tea.Model, tea.Cmd) {
		return m.openTreeTool()
	}},
	{"/usage", "show usage and timings", func(m Model, _ string) (tea.Model, tea.Cmd) {
		return m.openTool("usage", &stepRow{command: "/usage", toolKind: "usage", tool: toolState{usageExpand: -1}})
	}},
	{"/rollback", "undo a step and everything after it, e.g. /rollback 2", Model.runRollback},
	{"/new", "clear the session and start over", Model.startOver},
	{"/help", "show slash commands", func(m Model, _ string) (tea.Model, tea.Cmd) {
		return m.openTool("help", &stepRow{command: "/help", toolKind: "help"})
	}},
}

// maxSlashRows caps how many entries the dropdown shows at once. Past
// it the list scrolls rather than growing into the history pane.
const maxSlashRows = 6

// matchSlash returns registry entries with the given input as a
// prefix. Input must start with "/"; anything else matches nothing.
func matchSlash(input string) []slashCmd {
	if !strings.HasPrefix(input, "/") {
		return nil
	}
	var out []slashCmd
	for _, c := range slashCommands {
		if strings.HasPrefix(c.Name, strings.ToLower(input)) {
			out = append(out, c)
		}
	}
	return out
}

// exactSlash reports whether input is exactly one registry command.
func exactSlash(input string) bool {
	_, ok := lookupSlash(input)
	return ok
}

// lookupSlash finds the command named by the first word of input.
func lookupSlash(input string) (slashCmd, bool) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return slashCmd{}, false
	}
	name := strings.ToLower(fields[0])
	for _, c := range slashCommands {
		if c.Name == name {
			return c, true
		}
	}
	return slashCmd{}, false
}

// slashWindow is the visible slice, derived from the cursor rather
// than stored beside it. The cursor rides the bottom edge once the
// list has scrolled.
func slashWindow(n, cursor int) (start, end int) {
	if n <= maxSlashRows {
		return 0, n
	}
	start = min(max(0, cursor-maxSlashRows+1), n-maxSlashRows)
	return start, start + maxSlashRows
}

// slashDropdown renders the visible slice of the match list, with a
// count of what's scrolled out of view so a hidden entry can't be
// mistaken for one that doesn't exist.
func slashDropdown(cmds []slashCmd, cursor int) string {
	start, end := slashWindow(len(cmds), cursor)
	var b strings.Builder
	for i, c := range cmds[start:end] {
		mark, style := "  ", styleGoal
		if start+i == cursor {
			mark, style = "▸ ", styleRowCursor
		}
		// Pad the plain name before styling: padding an already
		// ANSI-wrapped string counts the escape bytes toward the
		// width and silently drops the padding.
		label := style.Render(fmt.Sprintf("%-10s", c.Name))
		fmt.Fprintf(&b, "  %s%s %s\n", style.Render(mark), label, styleMuted.Render(c.Desc))
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
