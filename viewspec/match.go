package viewspec

import (
	"fmt"
	"regexp"
	"strings"
)

// matcher is a "field=value" filter. The zero value matches nothing;
// parseMatch("") returns one that matches everything.
type matcher struct {
	field string
	value string
	all   bool
}

func parseMatch(expr string) (matcher, error) {
	if expr == "" || expr == "*" {
		return matcher{all: true}, nil
	}
	field, value, ok := strings.Cut(expr, "=")
	if !ok {
		return matcher{}, fmt.Errorf("filter %q must be field=value", expr)
	}
	field = strings.TrimSpace(field)
	if field == "" {
		return matcher{}, fmt.Errorf("filter %q has no field", expr)
	}
	return matcher{field: field, value: strings.TrimSpace(value)}, nil
}

func (m matcher) match(r Row) bool {
	if m.all {
		return true
	}
	return r[m.field] == m.value
}

// count reports how many rows match.
func (m matcher) count(rows []Row) int {
	n := 0
	for _, r := range rows {
		if m.match(r) {
			n++
		}
	}
	return n
}

// placeholder is the only templating there is: a field name in braces,
// substituted from one row. No expressions, no pipelines. A spec you
// can read and fix by hand is the payoff for caching them to disk.
var placeholder = regexp.MustCompile(`\{([a-z_][a-z0-9_]*)\}`)

func substitute(tmpl string, r Row) (string, error) {
	var missing string
	out := placeholder.ReplaceAllStringFunc(tmpl, func(m string) string {
		name := m[1 : len(m)-1]
		v, ok := r[name]
		if !ok {
			missing = name
			return m
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("template references unknown field %q", missing)
	}
	return out, nil
}

// templateFields lists the field names a template references, so a
// widget can reject one naming a field the parse never produced.
func templateFields(tmpl string) []string {
	var out []string
	for _, m := range placeholder.FindAllStringSubmatch(tmpl, -1) {
		out = append(out, m[1])
	}
	return out
}
