package viewspec

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Standard returns a registry holding the built-in vocabulary. Each
// call returns a fresh copy, so one caller's registrations cannot leak
// into another's.
func Standard() *Registry { return standard.clone() }

var standard = func() *Registry {
	r := NewRegistry()
	must(r.Extractor("lines", newLinesExtractor))
	must(r.Extractor("columns", newColumnsExtractor))
	must(r.Extractor("json", newJSONExtractor))
	must(r.Extractor("fixed", newFixedExtractor))
	must(r.Extractor("pairs", newPairsExtractor))
	must(r.Extractor("delimited", newDelimitedExtractor))
	must(r.Extractor("indent", newIndentExtractor))
	must(r.Extractor("none", newNoneExtractor))
	must(r.Widget(RowKind, rowWidget{}))
	must(r.Widget("text", textWidget{}))
	must(r.Widget("table", tableWidget{}))
	must(r.Widget("list", listWidget{}))
	must(r.Widget("keyvalue", keyvalueWidget{}))
	must(r.Widget("meter", meterWidget{}))
	must(r.Widget("badges", badgesWidget{}))
	must(r.Widget("tree", treeWidget{}))
	must(r.Widget("sparkline", sparklineWidget{}))
	must(r.Widget("bar", barWidget{}))
	must(r.Widget("log", rawWidget{mode: "log"}))
	must(r.Widget("errors", rawWidget{mode: "errors"}))
	must(r.Widget("json", rawWidget{mode: "json"}))
	must(r.Widget("diff", rawWidget{mode: "diff"}))
	must(r.Widget("code", rawWidget{mode: "code"}))
	return r
}()

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// text is model-authored framing, so it renders faint. A generated
// heading must not be able to read as a finding.
type textWidget struct{}

func (textWidget) Validate(b Block, _ []string) error {
	if b.Title == "" {
		return fmt.Errorf("text needs a title")
	}
	return nil
}

func (textWidget) Draw(b Block, _ Data, f Frame) ([]string, error) {
	return []string{f.Paint.Paint(RoleFaint, f.Paint.Truncate(b.Title, f.Width))}, nil
}

type tableWidget struct{}

func (tableWidget) Validate(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	for _, c := range b.Columns {
		if err := needField(c.Field, fields); err != nil {
			return err
		}
	}
	return checkShared(b, fields)
}

func (tableWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	cols := tableColumns(b, d)
	if len(cols) == 0 {
		return nil, fmt.Errorf("no columns to draw")
	}
	widths := fitColumns(cols, d.Rows, f.Width, f.Paint)
	head := make([]string, len(cols))
	for i, c := range cols {
		head[i] = pad(f.Paint.Truncate(c.title(), widths[i]), widths[i], f.Paint)
	}
	lines := []string{f.Paint.Paint(RoleHeading, strings.Join(head, " "))}
	for i, r := range d.Rows {
		cells := make([]string, len(cols))
		for j, c := range cols {
			cells[j] = pad(f.Paint.Truncate(r[c.Field], widths[j]), widths[j], f.Paint)
		}
		line := strings.Join(cells, " ")
		role := accentRole(b, r)
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		lines = append(lines, f.Paint.Paint(role, line))
	}
	return lines, nil
}

type listWidget struct{}

func (listWidget) Validate(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	if err := needField(b.Field, fields); err != nil {
		return err
	}
	return checkShared(b, fields)
}

func (listWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	lines := make([]string, 0, len(d.Rows))
	for i, r := range d.Rows {
		role := accentRole(b, r)
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		lines = append(lines, f.Paint.Paint(role, f.Paint.Truncate(r[b.Field], f.Width)))
	}
	return lines, nil
}

// keyvalue reads the first two columns as label and value, one pair
// per row, aligned on the widest label.
type keyvalueWidget struct{}

func (keyvalueWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 2 {
		return fmt.Errorf("keyvalue needs exactly two columns")
	}
	if len(fields) == 0 {
		return ErrNoRows
	}
	for _, c := range b.Columns {
		if err := needField(c.Field, fields); err != nil {
			return err
		}
	}
	return checkShared(b, fields)
}

func (keyvalueWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	key, val := b.Columns[0].Field, b.Columns[1].Field
	w := 0
	for _, r := range d.Rows {
		if n := f.Paint.Width(r[key]); n > w {
			w = n
		}
	}
	w = min(w, f.Width/2)
	lines := make([]string, 0, len(d.Rows))
	for i, r := range d.Rows {
		role := accentRole(b, r)
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		label := f.Paint.Paint(RoleMuted, pad(f.Paint.Truncate(r[key], w), w, f.Paint))
		lines = append(lines, label+"  "+
			f.Paint.Paint(role, f.Paint.Truncate(r[val], max(1, f.Width-w-2))))
	}
	return lines, nil
}

// meter draws a proportion counted from rows. The numbers come from
// here, never from the spec. A Title containing a number cannot
// change what the bar says.
type meterWidget struct{}

func (meterWidget) Validate(b Block, fields []string) error {
	if b.CountWhere == "" {
		return fmt.Errorf("meter needs count_where")
	}
	if len(fields) == 0 {
		return ErrNoRows
	}
	m, err := parseMatch(b.CountWhere)
	if err != nil {
		return err
	}
	if !m.all {
		return needField(m.field, fields)
	}
	return nil
}

const meterCells = 20

func (meterWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	hit, _ := parseMatch(b.CountWhere)
	of, err := parseMatch(b.Of)
	if err != nil {
		return nil, err
	}
	n, total := hit.count(d.Rows), of.count(d.Rows)
	filled := 0
	if total > 0 {
		filled = n * meterCells / total
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", meterCells-filled)
	role := RoleSafe
	if n < total {
		role = RoleCaution
	}
	label := b.Title
	if label == "" {
		label = b.CountWhere
	}
	line := f.Paint.Paint(RoleFaint, label+" ") +
		f.Paint.Paint(role, bar) +
		f.Paint.Paint(RoleDefault, fmt.Sprintf(" %d/%d", n, total))
	return []string{line}, nil
}

// badges summarises one field as its distinct values with counts.
type badgesWidget struct{}

func (badgesWidget) Validate(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	return needField(b.Field, fields)
}

func (badgesWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	counts := map[string]int{}
	for _, r := range d.Rows {
		counts[r[b.Field]]++
	}
	keys := slices.Sorted(maps.Keys(counts))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		role := RoleMuted
		if b.Accent != nil {
			if got, ok := b.Accent.Map[k]; ok {
				role = got
			}
		}
		parts = append(parts, f.Paint.Paint(role, fmt.Sprintf("%s %d", k, counts[k])))
	}
	return []string{strings.Join(parts, "  ")}, nil
}

// tableColumns is the block's own columns, or every parsed field in
// source order when it names none. That lets one table spec serve
// output whose columns are not known until it is parsed. A block
// column with no title of its own takes the parse's, so naming a
// column does not cost you the heading the output printed.
func tableColumns(b Block, d Data) []Column {
	if len(b.Columns) == 0 {
		return d.Columns
	}
	titles := make(map[string]string, len(d.Columns))
	for _, c := range d.Columns {
		if c.Title != "" {
			titles[c.Field] = c.Title
		}
	}
	out := make([]Column, len(b.Columns))
	for i, c := range b.Columns {
		if c.Title == "" {
			c.Title = titles[c.Field]
		}
		out[i] = c
	}
	return out
}

// rawWidget draws bytes: a log verbatim, a diff classified, code with
// a line gutter. It reads no fields, so it works under any parse.
type rawWidget struct{ mode string }

func (rawWidget) Validate(Block, []string) error { return nil }

func (w rawWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	body := d.Raw
	if w.mode == "json" {
		if pretty, ok := indentJSON(body); ok {
			body = pretty
		}
	}
	src := splitLines(body)
	if f.Height > 0 && len(src) > f.Height {
		src = src[len(src)-f.Height:]
	}
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

// checkShared validates the bindings any row widget may carry.
func checkShared(b Block, fields []string) error {
	if b.Accent != nil {
		if err := needField(b.Accent.Field, fields); err != nil {
			return err
		}
	}
	if b.Sort != nil {
		if err := needField(b.Sort.Field, fields); err != nil {
			return err
		}
	}
	for _, name := range templateFields(b.OnEnter) {
		if err := needField(name, fields); err != nil {
			return err
		}
	}
	if m, err := parseMatch(b.Where); err == nil && !m.all {
		if err := needField(m.field, fields); err != nil {
			return err
		}
	}
	return nil
}

func needField(name string, fields []string) error {
	if name == "" {
		return &BindError{Err: fmt.Errorf("needs a field")}
	}
	if slices.Contains(fields, name) {
		return nil
	}
	return &BindError{Field: name,
		Err: fmt.Errorf("not produced by the parse (have %s)", strings.Join(fields, ", "))}
}

func accentRole(b Block, r Row) Role {
	if b.Accent == nil {
		return RoleDefault
	}
	if role, ok := b.Accent.Map[r[b.Accent.Field]]; ok {
		return role
	}
	return RoleDefault
}

// fitColumns shares width proportionally to the widest cell, with a
// floor so every column stays visible. Cells truncate; the pane never
// scrolls sideways.
func fitColumns(cols []Column, rows []Row, total int, p Painter) []int {
	const floor = 4
	n := len(cols)
	widest := make([]int, n)
	for i, c := range cols {
		widest[i] = p.Width(c.title())
		for _, r := range rows {
			if w := p.Width(r[c.Field]); w > widest[i] {
				widest[i] = w
			}
		}
		if cols[i].Width > 0 {
			widest[i] = cols[i].Width
		}
	}
	avail := total - (n - 1)
	sum := 0
	for _, w := range widest {
		sum += w
	}
	if sum <= avail || sum == 0 {
		return widest
	}
	out := make([]int, n)
	rest := avail
	for i, w := range widest {
		out[i] = max(floor, w*avail/sum)
		rest -= out[i]
	}
	for i := 0; rest < 0; i, rest = (i+1)%n, rest+1 {
		if out[i] > floor {
			out[i]--
		}
	}
	return out
}

func pad(s string, w int, p Painter) string {
	if n := p.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// The two row widgets draw a cursor, so both report where it landed.
// A table's header sits above its rows; a list's does not.
func (tableWidget) CursorLine(_ Block, d Data, f Frame) int {
	if n := rowCursor(d, f); n >= 0 {
		return n + 1 // the header sits above the rows
	}
	return -1
}

func (listWidget) CursorLine(_ Block, d Data, f Frame) int { return rowCursor(d, f) }

// treeWidget draws a hierarchy. Depth names the field holding each
// row's level; without one, Field is read as a path and the levels
// come from its slashes.
type treeWidget struct{}

func (treeWidget) Validate(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	if err := needField(b.Field, fields); err != nil {
		return err
	}
	if b.Depth != "" {
		if err := needField(b.Depth, fields); err != nil {
			return err
		}
	}
	return checkShared(b, fields)
}

func (treeWidget) CursorLine(_ Block, d Data, f Frame) int { return rowCursor(d, f) }

func (treeWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	depths := treeDepths(b, d.Rows)
	lines := make([]string, 0, len(d.Rows))
	for i, r := range d.Rows {
		role := accentRole(b, r)
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		label := r[b.Field]
		if b.Depth == "" {
			label = leaf(label)
		}
		stem := f.Paint.Paint(RoleFaint, treeStem(depths, i))
		lines = append(lines, stem+f.Paint.Paint(role,
			f.Paint.Truncate(label, max(1, f.Width-2*depths[i]-2))))
	}
	return lines, nil
}

func treeDepths(b Block, rows []Row) []int {
	out := make([]int, len(rows))
	for i, r := range rows {
		if b.Depth != "" {
			n, _ := strconv.Atoi(r[b.Depth])
			out[i] = max(0, n)
			continue
		}
		out[i] = strings.Count(strings.Trim(r[b.Field], "/"), "/")
	}
	return out
}

// treeStem is row i's connector: a branch for its own level, and for
// each ancestor a bar only where that ancestor still has rows to come.
// Without that check a closed branch keeps trailing a line down the page.
func treeStem(depths []int, i int) string {
	d := depths[i]
	if d == 0 {
		return ""
	}
	var b strings.Builder
	for level := 1; level < d; level++ {
		if hasLaterSibling(depths, i, level) {
			b.WriteString("│  ")
			continue
		}
		b.WriteString("   ")
	}
	if hasLaterSibling(depths, i, d) {
		return b.String() + "├─ "
	}
	return b.String() + "└─ "
}

// hasLaterSibling reports whether another row at level appears before
// the tree returns to something shallower.
func hasLaterSibling(depths []int, i, level int) bool {
	for j := i + 1; j < len(depths); j++ {
		if depths[j] < level {
			return false
		}
		if depths[j] == level {
			return true
		}
	}
	return false
}

func leaf(path string) string {
	trimmed := strings.Trim(path, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

// sparklineWidget draws one numeric field as a bar strip, scaled to
// the values present rather than to zero. The shape is the point.
type sparklineWidget struct{}

func (sparklineWidget) Validate(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	return needField(b.Field, fields)
}

var sparkCells = []rune("▁▂▃▄▅▆▇█")

func (sparklineWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	values := make([]float64, 0, len(d.Rows))
	for _, r := range d.Rows {
		values = append(values, parseFloat(r[b.Field]))
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("no values to plot")
	}
	lo, hi := values[0], values[0]
	for _, v := range values {
		lo, hi = min(lo, v), max(hi, v)
	}
	var strip strings.Builder
	for _, v := range values {
		i := 0
		if hi > lo {
			i = int((v - lo) / (hi - lo) * float64(len(sparkCells)-1))
		}
		strip.WriteRune(sparkCells[i])
	}
	line := f.Paint.Paint(RoleAccent, strip.String())
	if b.Title != "" {
		line = f.Paint.Paint(RoleFaint, b.Title+" ") + line
	}
	return []string{line + f.Paint.Paint(RoleFaint,
		fmt.Sprintf("  %g–%g", lo, hi))}, nil
}

// barWidget charts one row per bar: Columns[0] labels, Columns[1] is
// the number. Bars scale to the largest value and to the frame, so the
// comparison survives a narrow pane.
type barWidget struct{}

func (barWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 2 {
		return fmt.Errorf("bar needs a label column and a value column")
	}
	if len(fields) == 0 {
		return ErrNoRows
	}
	for _, c := range b.Columns {
		if err := needField(c.Field, fields); err != nil {
			return err
		}
	}
	return checkShared(b, fields)
}

func (barWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	label, value := b.Columns[0].Field, b.Columns[1].Field
	labelW, hi := 0, 0.0
	for _, r := range d.Rows {
		labelW = max(labelW, f.Paint.Width(r[label]))
		hi = max(hi, parseFloat(r[value]))
	}
	labelW = min(labelW, f.Width/3)
	numW := 0
	for _, r := range d.Rows {
		numW = max(numW, f.Paint.Width(r[value]))
	}
	barW := f.Width - labelW - numW - 3
	if barW < 1 {
		return nil, fmt.Errorf("no width to chart in")
	}
	lines := make([]string, 0, len(d.Rows))
	for i, r := range d.Rows {
		n := 0
		if hi > 0 {
			n = int(parseFloat(r[value]) / hi * float64(barW))
		}
		role := accentOr(b, r, RoleAccent)
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		lines = append(lines,
			f.Paint.Paint(RoleMuted, pad(f.Paint.Truncate(r[label], labelW), labelW, f.Paint))+" "+
				f.Paint.Paint(role, strings.Repeat("█", n))+
				strings.Repeat(" ", barW-n)+" "+
				f.Paint.Paint(RoleFaint, r[value]))
	}
	return lines, nil
}

// accentOr is accentRole with a fallback for widgets whose resting
// colour is not RoleDefault.
func accentOr(b Block, r Row, fallback Role) Role {
	if b.Accent == nil {
		return fallback
	}
	return accentRole(b, r)
}

// What each widget is for, and the one it is most likely confused
// with. These reach the model through Registry.Schema.

func (textWidget) Describe() Description {
	return Description{
		What:     "one short label, drawn dim because it is your prose rather than output",
		Needs:    []string{"title"},
		NotFor:   "anything counted or measured — a meter computes its numbers, a title cannot",
		Examples: []string{"a heading above a table"},
	}
}

func (tableWidget) Describe() Description {
	return Description{
		What:     "rows in aligned columns, for reading several fields per record",
		NotFor:   "comparing one number across rows, where bar shows the shape at a glance",
		Examples: []string{"docker ps", "ps aux", "a package list with status and duration"},
	}
}

func (listWidget) Describe() Description {
	return Description{
		What:     "one field per line, for a set of names or paths",
		Needs:    []string{"field"},
		NotFor:   "paths whose nesting matters — tree draws that",
		Examples: []string{"changed files", "branch names"},
	}
}

func (keyvalueWidget) Describe() Description {
	return Description{
		What:     "label and value per row, aligned on the label",
		Needs:    []string{"columns (exactly two: label, then value)"},
		NotFor:   "many records of the same shape, which is a table",
		Examples: []string{"env", "git config -l", "one object's fields"},
	}
}

func (treeWidget) Describe() Description {
	return Description{
		What:     "a hierarchy, from a depth field or from a path's slashes",
		Needs:    []string{"field"},
		NotFor:   "a flat set of names with no nesting, which is a list",
		Examples: []string{"tree", "find . -name '*.go'", "an indented outline"},
	}
}

func (meterWidget) Describe() Description {
	return Description{
		What:     "one proportion counted from the rows, as a bar and a fraction",
		Needs:    []string{"count_where as field=value, an exact match", "of"},
		NotFor:   "a value per row — that is bar",
		Examples: []string{"how many tests passed", "how many files are staged"},
	}
}

func (barWidget) Describe() Description {
	return Description{
		What:     "one bar per row, scaled to the largest, for comparing a number across rows",
		Needs:    []string{"columns (exactly two: label, then the number)"},
		NotFor:   "a single proportion of a whole, which is a meter",
		Examples: []string{"time per package", "size per directory"},
	}
}

func (sparklineWidget) Describe() Description {
	return Description{
		What:     "one compact strip showing the shape of a numeric field across rows",
		Needs:    []string{"field"},
		NotFor:   "comparing individual rows, where bar is readable and this is not",
		Examples: []string{"a latency series", "sizes over time"},
	}
}

func (badgesWidget) Describe() Description {
	return Description{
		What:     "each distinct value of one field with how many rows have it",
		Needs:    []string{"field"},
		NotFor:   "showing the rows themselves — this only summarises them",
		Examples: []string{"git status codes", "container states"},
	}
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

// rowWidget exists so a row is in Kinds, Schema and the widget guide
// like anything else. Draw is unreachable: the interpreter lays panes
// out itself, because a Widget never sees the registry.
type rowWidget struct{}

func (rowWidget) Draw(Block, Data, Frame) ([]string, error) {
	return nil, fmt.Errorf("a row is laid out by the interpreter, not drawn")
}

func (rowWidget) Describe() Description {
	return Description{
		What:     "lays its panes side by side, for putting a summary next to the thing it summarises",
		Needs:    []string{"panes (at least two)"},
		NotFor:   "blocks that simply follow one another — those stack without a row",
		Examples: []string{"a meter beside the table it counts", "a chart beside its legend"},
	}
}

// Every widget drawing one row per record reports its cursor, so a
// caller can scroll to a selection and enter can act on it.
func (barWidget) CursorLine(_ Block, d Data, f Frame) int { return rowCursor(d, f) }

func (keyvalueWidget) CursorLine(_ Block, d Data, f Frame) int { return rowCursor(d, f) }

func rowCursor(d Data, f Frame) int {
	if f.Cursor < 0 || f.Cursor >= len(d.Rows) {
		return -1
	}
	return f.Cursor
}
