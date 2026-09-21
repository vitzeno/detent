package viewspec

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// linesExtractor makes a row per line matching a named-capture
// regexp. The model writes the pattern but never evaluates it, so a
// field can be in the wrong place, never invented.
type linesExtractor struct {
	re    *regexp.Regexp
	order []Column
}

// Columns is the pattern's named captures, left to right.
func (e linesExtractor) Columns() []Column { return e.order }

func newLinesExtractor(p Parse) (Extractor, error) {
	if p.Pattern == "" {
		return nil, fmt.Errorf("parse kind %q needs a pattern", p.Kind)
	}
	re, err := regexp.Compile(p.Pattern)
	if err != nil {
		return nil, fmt.Errorf("pattern does not compile: %w", err)
	}
	var order []Column
	for _, n := range re.SubexpNames() {
		if n != "" {
			order = append(order, Column{Field: n})
		}
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("pattern has no named captures")
	}
	return skipping(p, linesExtractor{re: re, order: order}), nil
}

func (e linesExtractor) Extract(output string) ([]Row, error) {
	var rows []Row
	names := e.re.SubexpNames()
	for _, line := range splitLines(output) {
		m := e.re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		row := Row{}
		for i, n := range names {
			if n != "" && i < len(m) {
				row[n] = m[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// columnsExtractor splits whitespace-aligned output. Trailing fields
// join into the last column, where ps-shaped output puts free text.
type columnsExtractor struct {
	header bool
	fields []string
	order  *[]Column // filled by Extract, which is where a header is read
}

func (e columnsExtractor) Columns() []Column {
	if e.order != nil && len(*e.order) > 0 {
		return *e.order
	}
	out := make([]Column, len(e.fields))
	for i, f := range e.fields {
		out[i] = Column{Field: f}
	}
	return out
}

func newColumnsExtractor(p Parse) (Extractor, error) {
	if !p.Header && len(p.Fields) == 0 {
		return nil, fmt.Errorf("parse kind %q needs header or fields", p.Kind)
	}
	return skipping(p, columnsExtractor{header: p.Header, fields: p.Fields, order: new([]Column)}), nil
}

func (e columnsExtractor) Extract(output string) ([]Row, error) {
	var grid [][]string
	for _, line := range splitLines(output) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		grid = append(grid, strings.Fields(line))
	}
	if len(grid) == 0 {
		return nil, nil
	}
	names := e.fields
	titles := names
	if e.header {
		titles = grid[0]
		names = lower(titles)
		grid = grid[1:]
	}
	// Keys lowercase so a spec can name them predictably; titles keep
	// the output's own spelling, because that is what a header is.
	if e.order != nil {
		cols := make([]Column, len(names))
		for i, n := range names {
			cols[i] = Column{Field: n, Title: titles[i]}
		}
		*e.order = cols
	}
	if len(names) < 1 {
		return nil, fmt.Errorf("no column names")
	}
	var rows []Row
	for _, f := range grid {
		if len(f) < len(names) {
			continue
		}
		row := Row{}
		for i, n := range names {
			if i == len(names)-1 {
				row[n] = strings.Join(f[i:], " ")
				continue
			}
			row[n] = f[i]
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// jsonExtractor reads an array of flat objects, or one object as a
// single row. Values are rendered back to strings: a Row is bytes the
// output actually contained, not a typed tree.
type jsonExtractor struct{}

func newJSONExtractor(p Parse) (Extractor, error) { return skipping(p, jsonExtractor{}), nil }

func (jsonExtractor) Extract(output string) ([]Row, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, nil
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
		return jsonRows(arr), nil
	}
	var one map[string]any
	if err := json.Unmarshal([]byte(trimmed), &one); err != nil {
		return nil, fmt.Errorf("not a JSON object or array: %w", err)
	}
	return jsonRows([]map[string]any{one}), nil
}

func jsonRows(in []map[string]any) []Row {
	rows := make([]Row, 0, len(in))
	for _, m := range in {
		row := Row{}
		for k, v := range m {
			row[strings.ToLower(k)] = scalar(v)
		}
		rows = append(rows, row)
	}
	return rows
}

func scalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", t), "0"), ".")
	case bool:
		return fmt.Sprintf("%t", t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// noneExtractor produces no rows, for a view drawn from raw text only.
type noneExtractor struct{}

func newNoneExtractor(Parse) (Extractor, error) { return noneExtractor{}, nil }

func (noneExtractor) Extract(string) ([]Row, error) { return nil, nil }

// skipping drops Parse.Skip leading lines before the real extractor
// sees them, so every parse kind gets banner-skipping for free. It is
// a struct rather than an ExtractorFunc so it can forward ColumnOrder
// — a closure would silently swallow the wrapped extractor's order.
type skipExtractor struct {
	inner Extractor
	n     int
}

func (e skipExtractor) Extract(output string) ([]Row, error) {
	lines := splitLines(output)
	if e.n >= len(lines) {
		return nil, nil
	}
	return e.inner.Extract(strings.Join(lines[e.n:], "\n"))
}

func (e skipExtractor) Columns() []Column {
	if c, ok := e.inner.(ColumnOrder); ok {
		return c.Columns()
	}
	return nil
}

func skipping(p Parse, e Extractor) Extractor {
	if p.Skip <= 0 {
		return e
	}
	return skipExtractor{inner: e, n: p.Skip}
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func lower(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(s)
	}
	return out
}
