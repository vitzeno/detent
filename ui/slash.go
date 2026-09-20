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

// maxSlashRows caps the dropdown so it can't eat the history pane.
// Room for the whole registry: a list that hides its last entry is
// worse than one row less of history.
const maxSlashRows = 8

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

// slashDropdown renders the match list, honouring maxSlashRows.
func slashDropdown(cmds []slashCmd, cursor int) string {
	var b strings.Builder
	for i, c := range cmds {
		if i >= maxSlashRows {
			break
		}
		mark, style := "  ", styleGoal
		if i == cursor {
			mark, style = "▸ ", styleRowCursor
		}
		// Pad the plain name before styling: padding an already
		// ANSI-wrapped string counts the escape bytes toward the
		// width and silently drops the padding.
		label := style.Render(fmt.Sprintf("%-10s", c.Name))
		fmt.Fprintf(&b, "  %s%s %s\n", style.Render(mark), label, styleMuted.Render(c.Desc))
	}
	return b.String()
}
