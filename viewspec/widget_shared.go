package viewspec

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

var errNoWidth = errors.New("no width to chart in")

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

// checkColumns is the validation every multi-column widget shares: rows
// exist, each named column was parsed, and the shared keys resolve.
func checkColumns(b Block, fields []string) error {
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

func needField(name string, fields []string) error {
	if name == "" {
		return &BindError{Err: errors.New("needs a field")}
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

// accentOr is accentRole with a fallback for widgets whose resting
// colour is not RoleDefault.
func accentOr(b Block, r Row, fallback Role) Role {
	if b.Accent == nil {
		return fallback
	}
	return accentRole(b, r)
}

func rowCursor(d Data, f Frame) int {
	if f.Cursor < 0 || f.Cursor >= len(d.Rows) {
		return -1
	}
	return f.Cursor
}

// chartCursor is rowCursor shifted past the title, since every chart
// here draws one when the block names it.
func chartCursor(b Block, d Data, f Frame) int {
	at := rowCursor(d, f)
	if at < 0 || b.Title == "" {
		return at
	}
	return at + 1
}

// fitColumns gives each column its widest cell, and when they do not
// fit, cuts only the widest. Cells truncate, never scrolling sideways.
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
	// Not shared in proportion, so one long column cannot squeeze a short one.
	limit := fairLimit(widest, avail)
	out := make([]int, n)
	rest := avail
	for i, w := range widest {
		out[i] = max(min(floor, w), min(w, limit))
		rest -= out[i]
	}
	for i := 0; rest > 0 && i < n; i++ {
		if widest[i] > out[i] {
			out[i]++
			rest--
		}
	}
	// Stops once every column is at its floor, and Draw clips the rest.
	for shrunk := true; rest < 0 && shrunk; {
		shrunk = false
		for i := 0; i < n && rest < 0; i++ {
			if out[i] > floor {
				out[i]--
				rest++
				shrunk = true
			}
		}
	}
	return out
}

// fairLimit is the widest any column may be so all of them fit in
// avail, with every column narrower than it drawn whole.
func fairLimit(widest []int, avail int) int {
	lo, hi := 0, slices.Max(widest)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		used := 0
		for _, w := range widest {
			used += min(w, mid)
		}
		if used <= avail {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// labelCell is a row's label, cut or padded to exactly w and drawn muted.
func labelCell(s string, w int, f Frame) string {
	return f.Paint.Paint(RoleMuted, fit(s, w, f.Paint))
}

// fit cuts or pads s to exactly w cells.
func fit(s string, w int, p Painter) string { return pad(p.Truncate(s, w), w, p) }

func pad(s string, w int, p Painter) string {
	if n := p.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// barLayout is the label, bar and value columns every row-per-bar
// widget shares, so stacking two of them in one view lines them up.
type barLayout struct{ label, bar, note int }

func layOutBars(labels, notes []string, f Frame) (barLayout, error) {
	var l barLayout
	for _, s := range labels {
		l.label = max(l.label, f.Paint.Width(s))
	}
	for _, s := range notes {
		l.note = max(l.note, f.Paint.Width(s))
	}
	l.label = min(l.label, f.Width/3)
	l.bar = f.Width - l.label - l.note - 2
	if l.bar < 1 {
		return l, errNoWidth
	}
	return l, nil
}

func (l barLayout) row(label string, filled int, role Role, note string, f Frame) string {
	filled = min(max(filled, 0), l.bar)
	return labelCell(label, l.label, f) + " " +
		f.Paint.Paint(role, strings.Repeat("█", filled)) +
		strings.Repeat(" ", l.bar-filled) + " " +
		f.Paint.Paint(RoleFaint, pad(note, l.note, f.Paint))
}

// titleLine starts a chart's lines with its label, or with nothing.
func titleLine(b Block, f Frame) []string {
	if b.Title == "" {
		return nil
	}
	return []string{f.Paint.Paint(RoleFaint, f.Paint.Truncate(b.Title, f.Width))}
}

// repeat is strings.Repeat that draws nothing for a negative count,
// which any scaled negative number gives.
func repeat(s string, n int) string { return strings.Repeat(s, max(n, 0)) }

// bounds is the span of v, and 0..0 when there is nothing in it.
func bounds(v []float64) (lo, hi float64) {
	if len(v) == 0 {
		return 0, 0
	}
	lo, hi = v[0], v[0]
	for _, x := range v {
		lo, hi = min(lo, x), max(hi, x)
	}
	return lo, hi
}

// fraction places v in 0..1 across the span, centring it when every
// value is the same rather than dividing by nothing.
func fraction(v, lo, hi float64) float64 {
	if hi <= lo {
		return 0.5
	}
	return (v - lo) / (hi - lo)
}

// groupValues buckets the rows by the first column, keeping the order
// each group was first seen in, which is the order the command chose.
func groupValues(b Block, d Data) (map[string][]float64, []string) {
	key, value := b.Columns[0].Field, b.Columns[1].Field
	groups := map[string][]float64{}
	var order []string
	for _, r := range d.Rows {
		if _, seen := groups[r[key]]; !seen {
			order = append(order, r[key])
		}
		groups[r[key]] = append(groups[r[key]], number(r[value]))
	}
	return groups, order
}
