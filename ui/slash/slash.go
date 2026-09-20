// Package slash owns input-bar commands end to end: the registry,
// prefix matching, and the autocomplete dropdown. The model drives it
// (updateSlash on every keystroke, acceptSlash on tab/enter) and renders
// View above the input bar.
package slash

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/theme"
)

// MaxRows caps the dropdown so it can't eat the history.
const MaxRows = 6

// Cmd is one available command.
type Cmd struct {
	Name string
	Desc string
}

var commands = []Cmd{
	{"/quit", "quit detent"},
	{"/abort", "abort the running command"},
	{"/tree", "browse files and directories"},
	{"/usage", "show usage and timings"},
	{"/rollback", "undo a step and everything after it, e.g. /rollback 2"},
	{"/help", "show slash commands"},
}

// Match returns registry entries with the given input as a prefix.
// Input must start with "/"; anything else matches nothing.
func Match(input string) []Cmd {
	if !strings.HasPrefix(input, "/") {
		return nil
	}
	var out []Cmd
	for _, c := range commands {
		if strings.HasPrefix(c.Name, strings.ToLower(input)) {
			out = append(out, c)
		}
	}
	return out
}

// Exact reports whether input is exactly one registry command.
func Exact(input string) bool {
	for _, c := range commands {
		if c.Name == strings.ToLower(input) {
			return true
		}
	}
	return false
}

// View renders the dropdown, honouring MaxRows.
func View(cmds []Cmd, cursor int) string {
	name := lipgloss.NewStyle().Foreground(theme.TextPrimary)
	hl := lipgloss.NewStyle().Foreground(theme.Accent).Bold(true)
	desc := lipgloss.NewStyle().Foreground(theme.TextMuted)
	var b strings.Builder
	for i, c := range cmds {
		if i >= MaxRows {
			break
		}
		mark, style := "  ", name
		if i == cursor {
			mark, style = "▸ ", hl
		}
		// Pad the plain name before styling: padding an already
		// ANSI-wrapped string counts the escape bytes toward the
		// width and silently drops the padding.
		label := style.Render(fmt.Sprintf("%-10s", c.Name))
		fmt.Fprintf(&b, "  %s%s %s\n", style.Render(mark), label, desc.Render(c.Desc))
	}
	return b.String()
}
