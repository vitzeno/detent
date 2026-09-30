package viewspec

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// linesExtractor makes a row per line matching a named-capture
// regexp. The model writes the pattern but never evaluates it, so a
// field can be in the wrong place, never invented.
type linesExtractor struct {
	re    *regexp.Regexp
	order []Column
}

// Columns is the pattern's named captures, left to right.
// Column order is an optional extension found by assertion, so a
// renamed method here would quietly fall back to alphabetical keys
// and lose the output's own spelling. That has happened once.
var (
	_ ColumnOrder = linesExtractor{}
	_ ColumnOrder = columnsExtractor{}
	_ ColumnOrder = skipExtractor{}
	_ ColumnOrder = fixedExtractor{}
	_ ColumnOrder = pairsExtractor{}
	_ ColumnOrder = delimitedExtractor{}
	_ ColumnOrder = indentExtractor{}
	_ ColumnOrder = boxExtractor{}
)

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
	lines := splitLines(output)
	if e.header {
		lines = headed(lines)
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || isRule(line) {
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
	// Most lines short of the header means the header was misread: df's
	// "Mounted on" names one column too many, and the rows kept are wrong.
	if len(rows)*2 < len(grid) {
		return nil, fmt.Errorf("%d of %d lines have fewer fields than the %d columns named",
			len(grid)-len(rows), len(grid), len(names))
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
	if err := json.Unmarshal([]byte(trimmed), &one); err == nil {
		return jsonRows([]map[string]any{one}), nil
	}
	// One object per line, which is what jq -c, docker inspect and
	// most structured logs print. Whole-document parsing rejects it.
	objs, err := jsonLines(trimmed)
	if err != nil {
		return nil, fmt.Errorf("not JSON, a JSON array, or one object per line: %w", err)
	}
	return jsonRows(objs), nil
}

// jsonLines reads newline-delimited objects. Every line must be one,
// so a truncated stream fails rather than drawing the half it liked.
func jsonLines(s string) ([]map[string]any, error) {
	var out []map[string]any
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no objects")
	}
	return out, nil
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

// skipExtractor drops leading lines so every parse kind gets banner
// skipping free. A struct, not a closure, so it forwards ColumnOrder.
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

// fixedExtractor slices rows between the values they line up, which
// reads "CONTAINER ID" as one column where whitespace fields see two.
type fixedExtractor struct{ order *[]Column }

func newFixedExtractor(p Parse) (Extractor, error) {
	return skipping(p, fixedExtractor{order: new([]Column)}), nil
}

type span struct {
	title string
	start int
	end   int // -1 runs to the end of the line
}

// gutterSpans lets the rows decide the cuts and hangs each header word
// on the column it is most over: cut at titles, 926Gi read as 9|26Gi.
func gutterSpans(header []rune, rows [][]rune) []span {
	cols := valueSpans(rows)
	words := wordsOf(header)
	at := make([]int, len(words))
	for i, w := range words {
		at[i] = -1
		most := 0
		for c, col := range cols {
			if n := min(w.end, col.end) - max(w.start, col.start); n > most {
				at[i], most = c, n
			}
		}
	}
	// A word with nothing under it belongs to the word one space away:
	// "CONTAINER ID" over a short id, "DISK USAGE" over a right-aligned one.
	for i := range words {
		if at[i] < 0 && i > 0 && at[i-1] >= 0 && words[i].start-words[i-1].end == 1 {
			at[i] = at[i-1]
		}
	}
	for i := len(words) - 1; i >= 0; i-- {
		if at[i] < 0 && i+1 < len(words) && at[i+1] >= 0 && words[i+1].start-words[i].end == 1 {
			at[i] = at[i+1]
		}
	}
	// What is left titles a column no row has a value in, like PORTS.
	valued := len(cols)
	for i := range words {
		if at[i] >= 0 {
			continue
		}
		if i > 0 && words[i].start-words[i-1].end == 1 && at[i-1] >= valued {
			at[i] = at[i-1]
			continue
		}
		cols = append(cols, span{start: words[i].start, end: words[i].end})
		at[i] = len(cols) - 1
	}
	first := make(map[int]int)
	for i := range words {
		if _, ok := first[at[i]]; !ok {
			first[at[i]] = i
		}
	}
	var out []span
	for c, col := range cols {
		i, ok := first[c]
		if !ok && c == 0 {
			// free -h labels its rows under a blank header.
			out = append(out, span{title: "col1", start: col.start})
			continue
		}
		if !ok {
			continue // no title of its own: sliced into the column before it
		}
		last := i
		for j := range words {
			if at[j] == c {
				last = j
			}
		}
		out = append(out, span{title: string(header[words[i].start:words[last].end]),
			start: min(col.start, words[i].start)})
	}
	slices.SortFunc(out, func(a, b span) int { return a.start - b.start })
	for i := range out {
		out[i].end = -1
		if i+1 < len(out) {
			out[i].end = out[i+1].start
		}
	}
	return out
}

// valueSpans is where the rows have values: runs no row leaves blank,
// cut wherever no row has a value on both sides.
func valueSpans(rows [][]rune) []span {
	width := 0
	for _, r := range rows {
		width = max(width, len(r))
	}
	set := func(r []rune, i int) bool { return i < len(r) && r[i] != ' ' }
	used := func(i int) bool {
		return slices.ContainsFunc(rows, func(r []rune) bool { return set(r, i) })
	}
	bridged := func(i int) bool {
		return slices.ContainsFunc(rows, func(r []rune) bool { return set(r, i-1) && set(r, i) })
	}
	var out []span
	for i := 0; i < width; {
		if !used(i) {
			i++
			continue
		}
		start := i
		i++
		for i < width && used(i) && bridged(i) {
			i++
		}
		out = append(out, span{start: start, end: i})
	}
	return out
}

// wordsOf is each space-separated word of a header and where it sits.
func wordsOf(header []rune) []span {
	var out []span
	for i := 0; i < len(header); {
		if header[i] == ' ' {
			i++
			continue
		}
		start := i
		for i < len(header) && header[i] != ' ' {
			i++
		}
		out = append(out, span{title: string(header[start:i]), start: start, end: i})
	}
	return out
}

func (e fixedExtractor) Columns() []Column {
	if e.order == nil {
		return nil
	}
	return *e.order
}

func (e fixedExtractor) Extract(output string) ([]Row, error) {
	lines := headed(splitLines(output))
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil, nil
	}
	var body [][]rune
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) != "" && !isRule(line) {
			body = append(body, []rune(line))
		}
	}
	spans := gutterSpans([]rune(lines[0]), body)
	if len(spans) < 2 {
		return nil, fmt.Errorf("header has fewer than two columns")
	}
	if e.order != nil {
		cols := make([]Column, len(spans))
		for i, s := range spans {
			cols[i] = Column{Field: strings.ToLower(s.title), Title: s.title}
		}
		*e.order = cols
	}
	rows := make([]Row, 0, len(body))
	for _, runes := range body {
		row := Row{}
		for _, s := range spans {
			row[strings.ToLower(s.title)] = strings.TrimSpace(slice(runes, s.start, s.end))
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func slice(r []rune, start, end int) string {
	if start >= len(r) {
		return ""
	}
	if end < 0 || end > len(r) {
		end = len(r)
	}
	return string(r[start:end])
}

// pairsExtractor reads "key<sep>value" lines: env, git config, any
// describe-shaped output. One row per line, keyed key and value.
type pairsExtractor struct{ sep string }

func newPairsExtractor(p Parse) (Extractor, error) {
	if p.Sep == "" {
		return nil, fmt.Errorf("parse kind %q needs a sep", p.Kind)
	}
	return skipping(p, pairsExtractor{sep: p.Sep}), nil
}

func (pairsExtractor) Columns() []Column {
	return []Column{{Field: "key"}, {Field: "value"}}
}

// prefix splits each line at the first run of whitespace, which is the
// shape git log, du, wc and go test all print without a header. Two
// fields and no more: a third would guess where the remainder divides,
// and a command needing that has columns or a pattern.
type prefixExtractor struct{}

func newPrefixExtractor(p Parse) (Extractor, error) { return skipping(p, prefixExtractor{}), nil }

func (prefixExtractor) Extract(output string) ([]Row, error) {
	var rows []Row
	for _, line := range splitLines(output) {
		first, rest := cutField(line)
		if first == "" {
			continue
		}
		rows = append(rows, Row{"first": first, "rest": rest})
	}
	return rows, nil
}

func (prefixExtractor) Columns() []Column {
	return []Column{{Field: "first"}, {Field: "rest"}}
}

// cutField takes the leading token and the remainder, each trimmed.
// Leading whitespace is skipped first, since wc right-aligns its counts
// and a blank first field would drop every line.
func cutField(line string) (first, rest string) {
	line = strings.TrimSpace(line)
	i := strings.IndexFunc(line, unicode.IsSpace)
	if i < 0 {
		return line, ""
	}
	return line[:i], strings.TrimSpace(line[i:])
}

func (e pairsExtractor) Extract(output string) ([]Row, error) {
	var rows []Row
	for _, line := range splitLines(output) {
		key, value, ok := strings.Cut(line, e.sep)
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		rows = append(rows, Row{"key": strings.TrimSpace(key), "value": strings.TrimSpace(value)})
	}
	return rows, nil
}

// delimitedExtractor splits on a separator rather than whitespace:
// CSV, TSV, /etc/passwd.
type delimitedExtractor struct {
	sep    string
	header bool
	fields []string
	order  *[]Column
}

func newDelimitedExtractor(p Parse) (Extractor, error) {
	if p.Sep == "" {
		return nil, fmt.Errorf("parse kind %q needs a sep", p.Kind)
	}
	if !p.Header && len(p.Fields) == 0 {
		return nil, fmt.Errorf("parse kind %q needs header or fields", p.Kind)
	}
	return skipping(p, delimitedExtractor{sep: p.Sep, header: p.Header,
		fields: p.Fields, order: new([]Column)}), nil
}

func (e delimitedExtractor) Columns() []Column {
	if e.order != nil && len(*e.order) > 0 {
		return *e.order
	}
	return plainColumns(e.fields)
}

func (e delimitedExtractor) Extract(output string) ([]Row, error) {
	var grid [][]string
	lines := splitLines(output)
	if e.header {
		lines = headed(lines)
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || isRule(line) {
			continue
		}
		parts := splitQuoted(line, e.sep)
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		grid = append(grid, parts)
	}
	if len(grid) == 0 {
		return nil, nil
	}
	names, titles := e.fields, e.fields
	if e.header {
		titles = grid[0]
		names = lower(titles)
		grid = grid[1:]
	}
	if e.order != nil {
		cols := make([]Column, len(names))
		for i, n := range names {
			cols[i] = Column{Field: n, Title: titles[i]}
		}
		*e.order = cols
	}
	var rows []Row
	for _, parts := range grid {
		row := Row{}
		for i, n := range names {
			if i < len(parts) {
				row[n] = parts[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// indentExtractor turns leading whitespace into a depth, so anything
// that prints a hierarchy by indenting it can be drawn as one. Levels
// come from the distinct indents actually present, which is what makes
// it work for two-space, four-space and tab output alike.
type indentExtractor struct{}

func newIndentExtractor(p Parse) (Extractor, error) {
	return skipping(p, indentExtractor{}), nil
}

func (indentExtractor) Columns() []Column {
	return []Column{{Field: "depth"}, {Field: "text"}}
}

func (indentExtractor) Extract(output string) ([]Row, error) {
	type entry struct {
		indent int
		text   string
	}
	var entries []entry
	seen := map[int]bool{}
	for _, line := range splitLines(output) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := 0
		for _, r := range line {
			switch r {
			case ' ':
				n++
			case '\t':
				n += 4
			default:
			}
			if r != ' ' && r != '\t' {
				break
			}
		}
		entries = append(entries, entry{indent: n, text: strings.TrimSpace(line)})
		seen[n] = true
	}
	levels := slices.Sorted(maps.Keys(seen))
	depthOf := make(map[int]int, len(levels))
	for i, l := range levels {
		depthOf[l] = i
	}
	rows := make([]Row, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, Row{"depth": strconv.Itoa(depthOf[e.indent]), "text": e.text})
	}
	return rows, nil
}

// splitQuoted splits a line the way CSV does, so a quoted "Smith, John"
// is one cell; split on the comma alone, it was two and shifted the row.
func splitQuoted(line, sep string) []string {
	r, size := utf8.DecodeRuneInString(sep)
	if size != len(sep) || r == '"' {
		return strings.Split(line, sep)
	}
	cr := csv.NewReader(strings.NewReader(line))
	cr.Comma, cr.LazyQuotes, cr.FieldsPerRecord = r, true, -1
	cells, err := cr.Read()
	if err != nil {
		return strings.Split(line, sep)
	}
	return cells
}

// headed is a headed table's lines, cut at the first blank line after
// its rows: systemctl's legend and top's second block are not rows.
func headed(lines []string) []string {
	start, rows := -1, 0
	for i, l := range lines {
		blank := strings.TrimSpace(l) == ""
		switch {
		case start < 0 && !blank:
			start = i
		case start >= 0 && blank && rows > 0:
			return lines[start:i]
		case start >= 0 && !blank && !isRule(l):
			rows++
		}
	}
	if start < 0 {
		return nil
	}
	return lines[start:]
}

// boxExtractor reads a table drawn with borders: mysql, psql, sqlite
// -box, markdown. Split on whitespace, every border became a column.
type boxExtractor struct{ order *[]Column }

func newBoxExtractor(p Parse) (Extractor, error) {
	return skipping(p, boxExtractor{order: new([]Column)}), nil
}

func (e boxExtractor) Columns() []Column { return *e.order }

// Extract takes the first bordered line as the header. A line with no
// border is a title or a footer, like psql's "(12 rows)".
func (e boxExtractor) Extract(output string) ([]Row, error) {
	var grid [][]string
	for _, line := range headed(splitLines(output)) {
		if isRule(line) {
			continue
		}
		if cells := boxCells(line); cells != nil {
			grid = append(grid, cells)
		}
	}
	if len(grid) == 0 {
		return nil, nil
	}
	titles, names := grid[0], lower(grid[0])
	cols := make([]Column, len(names))
	for i, n := range names {
		cols[i] = Column{Field: n, Title: titles[i]}
	}
	*e.order = cols
	rows := make([]Row, 0, len(grid)-1)
	for _, cells := range grid[1:] {
		row := Row{}
		for i, n := range names {
			if i < len(cells) {
				row[n] = cells[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// borders are what a drawn table puts between its cells.
const borders = "|│┃║"

// boxCells splits a line on its border, dropping what lies outside the
// first and last one. nil when the line has no border.
func boxCells(line string) []string {
	trimmed := strings.TrimSpace(line)
	i := strings.IndexAny(trimmed, borders)
	if i < 0 {
		return nil
	}
	border, _ := utf8.DecodeRuneInString(trimmed[i:])
	cells := strings.Split(trimmed, string(border))
	// Framed on both sides or not at all: psql's "2 | " ends in a
	// border only because its last cell is empty.
	b := string(border)
	if len(cells) > 2 && strings.HasPrefix(trimmed, b) && strings.HasSuffix(trimmed, b) {
		cells = cells[1 : len(cells)-1]
	}
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// isRule reports whether a line only rules a table off, as mysql's
// +----+, psql's ----+----, markdown's |---| and pip's ------- do.
func isRule(line string) bool {
	dashes := 0
	for _, r := range strings.TrimSpace(line) {
		switch {
		case strings.ContainsRune("-=─━═", r):
			dashes++
		case strings.ContainsRune("+|:┼├┤┌┐└┘┬┴╭╮╰╯╪╫╬╞╡╟╢╔╗╚╝╠╣╦╩│┃║ ", r):
		default:
			return false
		}
	}
	return dashes >= 3
}

func plainColumns(fields []string) []Column {
	out := make([]Column, len(fields))
	for i, f := range fields {
		out[i] = Column{Field: f}
	}
	return out
}
