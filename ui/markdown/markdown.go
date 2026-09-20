// Package markdown renders file prose through glamour. Jev's file
// kind decides WHEN; Wants gates glamour to actual markdown because it
// reads worse than plain text on anything else.
package markdown

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
)

var mdPathRe = regexp.MustCompile(`(?i)\.md(own)?\b`)

// Wants reports whether content deserves glamour: the command names a
// markdown file, or the body opens like one.
func Wants(command, output string) bool {
	if mdPathRe.MatchString(command) {
		return true
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return strings.HasPrefix(strings.TrimSpace(line), "#")
	}
	return false
}

// Render renders prose at the given width.
func Render(body string, width int) (string, error) {
	r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(max(20, width)))
	if err != nil {
		return "", err
	}
	return r.Render(body)
}
