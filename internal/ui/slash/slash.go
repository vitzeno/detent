// Package slash owns input-bar commands end to end: the registry,
// prefix matching, and the autocomplete dropdown. The model drives it
// (updateSlash on every keystroke, acceptSlash on tab/enter) and renders
// View above the input bar.
package slash

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/vitzeno/detent/internal/ui/theme"
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
	{"/usage", "show usage and timings"},
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
		mark, label := "  ", name.Render(c.Name)
		if i == cursor {
			mark, label = hl.Render("▸ "), hl.Render(c.Name)
		}
		fmt.Fprintf(&b, "  %s%-10s %s\n", mark, label, desc.Render(c.Desc))
	}
	return b.String()
}
