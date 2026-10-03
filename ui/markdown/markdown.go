// Package markdown renders file prose through glamour. Jev's file kind
// decides when, and Wants gates glamour to actual markdown because it
// reads worse than plain text on anything else.
package markdown

import (
	"path/filepath"
	"strings"

	"charm.land/glamour/v2"
)

var markdownExts = map[string]bool{".md": true, ".markdown": true, ".mdown": true, ".mkd": true}

// Wants reports whether content deserves glamour: the command names a
// markdown file, or names no file and the body reads like markdown.
func Wants(command, output string) bool {
	switch named(command) {
	case "markdown":
		return true
	case "other":
		// A YAML or shell file opening with a # comment is not a heading.
		return false
	}
	return looksLikeMarkdown(output)
}

// Render renders prose in the named glamour style rather than one sniffed
// from the terminal. Under 20 wide it still wraps at 20, and the frame truncates.
func Render(body, style string, width int) (string, error) {
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(style),
		glamour.WithWordWrap(max(20, width)),
	)
	if err != nil {
		return "", err
	}
	return r.Render(body)
}

// named says what kind of file a command names: "markdown", "other",
// or "" when it names no file at all.
func named(command string) string {
	kind := ""
	for field := range strings.FieldsSeq(command) {
		ext := strings.ToLower(filepath.Ext(strings.Trim(field, `'"`)))
		switch {
		case markdownExts[ext]:
			return "markdown"
		case len(ext) > 1 && strings.IndexFunc(ext[1:], notLetter) < 0:
			kind = "other"
		}
	}
	return kind
}

func notLetter(r rune) bool { return r < 'a' || r > 'z' }

// looksLikeMarkdown wants a heading first and one more thing only
// markdown says, since a lone # line opens most scripts and configs.
func looksLikeMarkdown(output string) bool {
	heading, signal := false, false
	for line := range strings.Lines(output) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !heading {
			if !strings.HasPrefix(line, "# ") && !strings.HasPrefix(line, "## ") {
				return false
			}
			heading = true
			continue
		}
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "```") ||
			strings.Contains(line, "](") || strings.Contains(line, "**") {
			signal = true
			break
		}
	}
	return heading && signal
}
