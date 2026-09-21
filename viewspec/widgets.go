package viewspec

import (
	"fmt"
	"maps"
	"slices"
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
	must(r.Extractor("none", newNoneExtractor))
	must(r.Widget("text", textWidget{}))
	must(r.Widget("table", tableWidget{}))
	must(r.Widget("list", listWidget{}))
	must(r.Widget("keyvalue", keyvalueWidget{}))
	must(r.Widget("meter", meterWidget{}))
	must(r.Widget("badges", badgesWidget{}))
	must(r.Widget("log", rawWidget{mode: "log"}))
	must(r.Widget("diff", rawWidget{mode: "diff"}))
	must(r.Widget("code", rawWidget{mode: "code"}))
	return r
}()

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// text is model-authored framing, so it renders faint — a generated
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
	if len(b.Columns) == 0 {
		return fmt.Errorf("table needs columns")
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

func (tableWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	widths := fitColumns(b.Columns, d.Rows, f.Width, f.Paint)
	head := make([]string, len(b.Columns))
	for i, c := range b.Columns {
		head[i] = pad(f.Paint.Truncate(c.title(), widths[i]), widths[i], f.Paint)
	}
	lines := []string{f.Paint.Paint(RoleHeading, strings.Join(head, " "))}
	for i, r := range d.Rows {
		cells := make([]string, len(b.Columns))
		for j, c := range b.Columns {
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
	for _, r := range d.Rows {
		label := f.Paint.Paint(RoleMuted, pad(f.Paint.Truncate(r[key], w), w, f.Paint))
		value := f.Paint.Paint(accentRole(b, r), f.Paint.Truncate(r[val], max(1, f.Width-w-2)))
		lines = append(lines, label+"  "+value)
	}
	return lines, nil
}

// meter draws a proportion counted from rows. The numbers come from
// here, never from the spec — a Title containing a number cannot
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

// rawWidget draws bytes: a log verbatim, a diff classified, code with
// a line gutter. It reads no fields, so it works under any parse.
type rawWidget struct{ mode string }

func (rawWidget) Validate(Block, []string) error { return nil }

func (w rawWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	src := splitLines(d.Raw)
	if f.Height > 0 && len(src) > f.Height {
		src = src[len(src)-f.Height:]
	}
	out := make([]string, 0, len(src))
	for i, l := range src {
		switch w.mode {
		case "diff":
			out = append(out, f.Paint.Paint(diffRole(l), f.Paint.Truncate(l, f.Width)))
		case "code":
			gutter := f.Paint.Paint(RoleFaint, fmt.Sprintf("%4d ", i+1))
			out = append(out, gutter+f.Paint.Truncate(l, max(1, f.Width-5)))
		default:
			out = append(out, f.Paint.Truncate(l, f.Width))
		}
	}
	return out, nil
}

// diffRole classifies one unified-diff line. Classification only —
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
	if f.Cursor < 0 || f.Cursor >= len(d.Rows) {
		return -1
	}
	return f.Cursor + 1
}

func (listWidget) CursorLine(_ Block, d Data, f Frame) int {
	if f.Cursor < 0 || f.Cursor >= len(d.Rows) {
		return -1
	}
	return f.Cursor
}
