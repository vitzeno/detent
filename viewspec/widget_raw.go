package viewspec

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// rawWidget draws bytes: a log verbatim, a diff classified, code with
// a line gutter. It reads no fields, so it works under any parse.
type rawWidget struct{ mode string }

var (
	_ Validator = rawWidget{}
	_ Described = rawWidget{}
)

func (rawWidget) Validate(Block, []string) error { return nil }

func (w rawWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	body := d.Raw
	if w.mode == "json" {
		if pretty, ok := indentJSON(body); ok {
			body = pretty
		}
	}
	src := splitLines(body)
	out := make([]string, 0, len(src))
	for i, l := range src {
		switch w.mode {
		case "diff":
			out = append(out, f.Paint.Paint(diffRole(l), f.Paint.Truncate(l, f.Width)))
		case "errors":
			out = append(out, f.Paint.Paint(severityRole(l), f.Paint.Truncate(l, f.Width)))
		case "code":
			gutter := f.Paint.Paint(RoleFaint, fmt.Sprintf("%4d ", i+1))
			out = append(out, gutter+f.Paint.Truncate(l, max(1, f.Width-5)))
		default:
			out = append(out, f.Paint.Truncate(l, f.Width))
		}
	}
	return out, nil
}

// diffRole classifies one unified-diff line. Classification only;
// which colour a role becomes is the consumer's business.
func diffRole(l string) Role {
	switch {
	case strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---"):
		return RoleMuted
	case strings.HasPrefix(l, "+"):
		return RoleSafe
	case strings.HasPrefix(l, "-"):
		return RoleDanger
	case strings.HasPrefix(l, "@@"):
		return RoleFaint
	case strings.HasPrefix(l, "diff ") || strings.HasPrefix(l, "index "):
		return RoleMuted
	}
	return RoleDefault
}

var (
	errorLine   = regexp.MustCompile(`(?i)\b(error|fail|panic|traceback|exception)\b`)
	warningLine = regexp.MustCompile(`(?i)\b(warn(ing)?|deprecat|retry)\b`)
)

// severityRole colours whole lines, not matches: a traceback reads as
// a unit, and half-painted lines read as noise.
func severityRole(l string) Role {
	switch {
	case errorLine.MatchString(l):
		return RoleDanger
	case warningLine.MatchString(l):
		return RoleCaution
	}
	return RoleDefault
}

// indentJSON pretty-prints valid JSON; anything else passes through.
func indentJSON(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", false
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		return "", false
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", false
	}
	return string(out), true
}

func (w rawWidget) Describe() Description {
	switch w.mode {
	case "errors":
		return Description{
			What:     "output whose point is a failure, coloured line by line by severity",
			NotFor:   "a long log that merely contains some warnings, which is log",
			Examples: []string{"a failed build", "a stack trace"},
		}
	case "json":
		return Description{
			What:     "JSON shown pretty-printed, read as data",
			NotFor:   "a few top-level fields a human reads as labels — that is keyvalue",
			Examples: []string{"an API response", "kubectl get -o json"},
		}
	case "diff":
		return Description{
			What:     "a unified diff, added and removed lines coloured",
			NotFor:   "prose describing changes; this needs the literal diff format",
			Examples: []string{"git diff", "diff -u a b"},
		}
	case "code":
		return Description{
			What:     "a file's own body, with a line-number gutter",
			NotFor:   "well-formed JSON, even from cat — that is the json widget",
			Examples: []string{"cat main.go"},
		}
	}
	return Description{
		What:     "the output verbatim, read top to bottom",
		NotFor:   "output with a shape worth drawing — reach for this when nothing else fits",
		Examples: []string{"a build log", "tail -n 200 app.log"},
	}
}
