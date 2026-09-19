package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Viewport content transforms for the detail zone. Jev's render_kind
// decides WHICH transform; everything here is deterministic code.

var (
	errorLineRe   = regexp.MustCompile(`(?i)\b(error|fail|panic|traceback|exception)\b`)
	warningLineRe = regexp.MustCompile(`(?i)\b(warn(ing)?|deprecat|retry)\b`)
)

// errorSeverity classifies a line for error styling: 2 for errors,
// 1 for warnings, 0 for plain lines. Split from styleErrorLine so the
// logic is testable without a terminal color profile.
func errorSeverity(l string) int {
	switch {
	case errorLineRe.MatchString(l):
		return 2
	case warningLineRe.MatchString(l):
		return 1
	default:
		return 0
	}
}

// styleErrorLine colors whole lines by severity: red for errors, amber
// for warnings, untouched otherwise. Whole lines, not matches — a
// traceback reads as a unit.
func styleErrorLine(l string) string {
	switch errorSeverity(l) {
	case 2:
		return styleDanger.Render(l)
	case 1:
		return styleCaution.Render(l)
	default:
		return l
	}
}

// diffClass classifies one unified-diff line: add, del, hunk, meta, ctx.
// Split from styleDiffLine for the same testability reason.
func diffClass(l string) string {
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

// styleDiffLine colors unified-diff lines: green added, red removed,
// faint hunk headers and metadata.
func styleDiffLine(l string) string {
	switch diffClass(l) {
	case "add":
		return lipgloss.NewStyle().Foreground(safe).Render(l)
	case "del":
		return lipgloss.NewStyle().Foreground(danger).Render(l)
	case "hunk":
		return styleFaint.Render(l)
	case "meta":
		return styleMuted.Render(l)
	default:
		return l
	}
}

// numberLines adds a faint fixed-width gutter. Display-only.
func numberLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = styleFaint.Render(fmt.Sprintf("%4d ", i+1)) + l
	}
	return out
}

// prettyJSON indents valid JSON; anything else passes through for the
// log viewport. Reports ok=false when the input isn't JSON at all.
func prettyJSON(s string) (string, bool) {
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
