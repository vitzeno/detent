package viewspec

import (
	"fmt"
	"regexp"
	"strings"
)

// matcher is a "field=value" filter. The zero value matches nothing,
// and parseMatch("") returns one that matches everything.
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

// countOf counts the rows of matches and how many of those hit, so a
// hit outside the denominator can never make n exceed total.
func countOf(hit, of matcher, rows []Row) (n, total int) {
	for _, r := range rows {
		if of.match(r) {
			total++
			if hit.match(r) {
				n++
			}
		}
	}
	return n, total
}

// checkCount validates count_where and of, each naming a parsed field.
func checkCount(b Block, fields []string) error {
	for _, expr := range []string{b.CountWhere, b.Of} {
		m, err := parseMatch(expr)
		if err != nil {
			return err
		}
		if !m.all {
			if err := needField(m.field, fields); err != nil {
				return err
			}
		}
	}
	return nil
}

// placeholder is the only templating there is: a field name in braces,
// substituted from one row, so a spec stays readable by hand.
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
	matches := placeholder.FindAllStringSubmatch(tmpl, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}
