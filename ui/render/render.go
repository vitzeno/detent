// Package render turns a command's captured output into what the
// detail pane shows. Jev's render_kind picks the transform; everything
// here is deterministic.
package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/theme"
)

var (
	faint   lipgloss.Style
	muted   lipgloss.Style
	caution lipgloss.Style
	danger  lipgloss.Style
	added   lipgloss.Style
	removed lipgloss.Style
)

func init() { RefreshStyles() }

// RefreshStyles rebuilds this package's styles from the current
// theme — call after theme.Apply.
func RefreshStyles() {
	faint = lipgloss.NewStyle().Foreground(theme.TextFaint)
	muted = lipgloss.NewStyle().Foreground(theme.TextMuted)
	caution = lipgloss.NewStyle().Foreground(theme.Caution).Bold(true)
	danger = lipgloss.NewStyle().Foreground(theme.Danger).Bold(true)
	added = lipgloss.NewStyle().Foreground(theme.Safe)
	removed = lipgloss.NewStyle().Foreground(theme.Danger)
}

var (
	errorLineRe   = regexp.MustCompile(`(?i)\b(error|fail|panic|traceback|exception)\b`)
	warningLineRe = regexp.MustCompile(`(?i)\b(warn(ing)?|deprecat|retry)\b`)
)

// ErrorSeverity classifies a line for error styling: 2 for errors,
// 1 for warnings, 0 for plain lines. Split from ErrorLine so the
// logic is testable without a terminal color profile.
func ErrorSeverity(l string) int {
	switch {
	case errorLineRe.MatchString(l):
		return 2
	case warningLineRe.MatchString(l):
		return 1
	default:
		return 0
	}
}

// ErrorLine colors whole lines by severity: red for errors, amber
// for warnings, untouched otherwise. Whole lines, not matches — a
// traceback reads as a unit.
func ErrorLine(l string) string {
	switch ErrorSeverity(l) {
	case 2:
		return danger.Render(l)
	case 1:
		return caution.Render(l)
	default:
		return l
	}
}

// DiffClass classifies one unified-diff line: add, del, hunk, meta, ctx.
// Split from DiffLine for the same testability reason.
func DiffClass(l string) string {
	switch {
	case strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
		return "add"
	case strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---"):
		return "del"
	case strings.HasPrefix(l, "@@"):
		return "hunk"
	case strings.HasPrefix(l, "diff ") || strings.HasPrefix(l, "index ") ||
		strings.HasPrefix(l, "---") || strings.HasPrefix(l, "+++"):
		return "meta"
	default:
		return "ctx"
	}
}

// DiffLine colors unified-diff lines: green added, red removed,
// faint hunk headers and metadata.
func DiffLine(l string) string {
	switch DiffClass(l) {
	case "add":
		return added.Render(l)
	case "del":
		return removed.Render(l)
	case "hunk":
		return faint.Render(l)
	case "meta":
		return muted.Render(l)
	default:
		return l
	}
}

// NumberLines adds a faint fixed-width gutter. Display-only.
func NumberLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = faint.Render(fmt.Sprintf("%4d ", i+1)) + l
	}
	return out
}

// JSON indents valid JSON; anything else passes through for the
// log viewport. Reports ok=false when the input isn't JSON at all.
func JSON(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", false
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		return "", false
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", false
	}
	return strings.TrimSuffix(buf.String(), "\n"), true
}
