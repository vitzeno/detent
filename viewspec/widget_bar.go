package viewspec

import (
	"errors"
	"strings"
)

// barWidget charts one row per bar: Columns[0] labels, Columns[1] is
// the number. Bars scale to the largest value and to the frame.
type barWidget struct{}

var (
	_ Validator = barWidget{}
	_ Selector  = barWidget{}
	_ Described = barWidget{}
)

func (barWidget) Describe() Description {
	return Description{
		What: "one bar per row, scaled to the largest, for comparing a number across rows",
		Needs: []Slot{
			{Name: "label", What: "labels each bar"},
			{Name: "value", What: "the number each bar is long by"},
		},
		NotFor:   "a single proportion of a whole, which is a meter",
		Examples: []string{"time per package", "size per directory"},
	}
}

func (barWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 2 {
		return errors.New("bar needs a label column and a value column")
	}
	return checkColumns(b, fields)
}

func (barWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	label, value := b.Columns[0].Field, b.Columns[1].Field
	labelW, hi := 0, 0.0
	for _, r := range d.Rows {
		labelW = max(labelW, f.Paint.Width(r[label]))
		hi = max(hi, number(r[value]))
	}
	labelW = min(labelW, f.Width/3)
	numW := 0
	for _, r := range d.Rows {
		numW = max(numW, f.Paint.Width(r[value]))
	}
	barW := f.Width - labelW - numW - 3
	if barW < 1 {
		return nil, errNoWidth
	}
	lines := make([]string, 0, len(d.Rows))
	for i, r := range d.Rows {
		n := 0
		if hi > 0 {
			n = min(max(int(number(r[value])/hi*float64(barW)), 0), barW)
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

func (barWidget) CursorLine(_ Block, d Data, f Frame) int { return rowCursor(d, f) }
